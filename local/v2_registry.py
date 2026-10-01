#!/usr/bin/env python3
"""Merge validated private 2.0 session proposals without inventing decisions.

Input: reports/rebuild/<session_id>/deep.v2.json for the frozen manifest.
Output: reports/rebuild/proposals.v2.json, validated before atomic replacement.
No raw session data or report text is printed.
"""

import copy
import json
import os
import shutil
import sys
import tempfile
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from analytics.v2_contract import validate_deep, validate_registry  # noqa: E402
from local.v2_lifecycle import DEFAULT_DECISION, DEFAULT_EFFECT, DEFAULT_EXECUTION, proposal_fingerprint, project_proposal, read_events  # noqa: E402


PRIORITY = {"low": 0, "medium": 1, "high": 2}
READY = {"hypothesis": 0, "needs_specification": 1, "prepared_verified": 2}


def select_source(manifest_source, v2_source, report):
    """Use the frozen version that matches the published Deep source hash."""
    if v2_source and report.get("source_sha256") == v2_source["source_sha256"]:
        return v2_source
    return manifest_source or v2_source


def load_manifest(root, published):
    """A new-session-only store need not contain a retrospective manifest."""
    path = root / "telemetry" / "manifest.json"
    try:
        raw = path.read_text()
    except FileNotFoundError:
        if published:
            return {"sessions": []}
        raise
    manifest = json.loads(raw)
    if not isinstance(manifest, dict) or not isinstance(manifest.get("sessions"), list):
        raise ValueError("invalid retrospective manifest")
    return manifest


def merge_candidates(reports, previous=None, lifecycle_events=None):
    """Group identical intents; expose conflicts instead of silently reconciling them."""
    groups = {}
    for sid in sorted(reports):
        report, _ = reports[sid]
        for candidate in report["proposal_candidates"]:
            if candidate["decision"]["status"] != "not_requested" or candidate["execution"]["status"] != "not_applied" or candidate["effect"]["status"] != "not_measured":
                raise ValueError(f"{sid}: analysis candidate cannot assert a human decision or applied effect")
            key = candidate["group_key"]
            if key not in groups:
                groups[key] = copy.deepcopy(candidate)
                groups[key]["proposal_id"] = key
                continue
            current = groups[key]
            conflicts = []
            for field in ("change_type", "scope", "change_intent", "change", "verification", "rollback", "expected_effect"):
                if current[field] != candidate[field]:
                    conflicts.append(field)
            uncertain_diagnosis = (current["change_type"] == candidate["change_type"] == "diagnosis"
                                   and current["readiness"]["status"] != "prepared_verified"
                                   and candidate["readiness"]["status"] != "prepared_verified")
            if current["cause"] != candidate["cause"]:
                if uncertain_diagnosis:
                    note = " Cause remains uncertain across source sessions; check it before preparing a change."
                    if note not in current.get("history_review", ""):
                        current["history_review"] = current.get("history_review", "") + note
                else:
                    conflicts.append("cause")
            if current["target"] != candidate["target"]:
                conflicts.append("target")
            if current["existing_rule"] != candidate["existing_rule"]:
                statuses = {current["existing_rule"]["status"], candidate["existing_rule"]["status"]}
                if uncertain_diagnosis and "not_checked" in statuses:
                    note = " Historical rule availability differs by session; verify current rules and scope."
                    if note not in current.get("history_review", ""):
                        current["history_review"] = current.get("history_review", "") + note
                    if candidate["existing_rule"]["status"] == "not_checked":
                        current["existing_rule"] = copy.deepcopy(candidate["existing_rule"])
                else:
                    conflicts.append("existing_rule")
            if conflicts:
                current["readiness"] = {"status": "needs_specification", "reason": "Conflicting session proposals: " + ", ".join(conflicts) + "; review before preparation."}
                current["history_review"] = current.get("history_review", "") + " Conflicting details from another session require semantic review."
            elif READY[candidate["readiness"]["status"]] < READY[current["readiness"]["status"]]:
                current["readiness"] = copy.deepcopy(candidate["readiness"])
            if PRIORITY[candidate["priority"]] > PRIORITY[current["priority"]]:
                current["priority"] = candidate["priority"]
            source_keys = {(s["session_id"], s["task_id"]) for s in current["sources"]}
            for source in candidate["sources"]:
                if (source["session_id"], source["task_id"]) not in source_keys:
                    current["sources"].append(copy.deepcopy(source))
                    source_keys.add((source["session_id"], source["task_id"]))
            for version in candidate["analysis_versions"]:
                if version not in current["analysis_versions"]:
                    current["analysis_versions"].append(version)
    if previous:
        previous_by_key = {p["group_key"]: p for p in previous["proposals"]}
        for key, current in groups.items():
            old = previous_by_key.get(key)
            if not old:
                continue
            if old["target"] != current["target"] or old["change"] != current["change"]:
                if current["readiness"]["status"] != "prepared_verified":
                    current["readiness"] = {"status": "needs_specification", "reason": "Target or change differs from previous decision; inspect current state."}
                current["history_review"] = current.get("history_review", "") + " Target or change differs from earlier lifecycle; a new owner decision is required."
                continue
    if lifecycle_events is not None:
        groups = {key: project_proposal(item, lifecycle_events) for key, item in groups.items()}
    for item in groups.values():
        session_count = len({source["session_id"] for source in item["sources"]})
        if session_count > 1:
            item["observed"] = (f"Похожие основания есть в {session_count} сессиях; ссылки приведены в карточке. "
                                f"Пример из первой: {item['observed']}")
    return {"kind": "proposal_registry", "schema_version": 2,
            "proposals": sorted(groups.values(), key=lambda p: (-PRIORITY[p["priority"]], p["group_key"]))}


def private_json(path):
    if not path.is_file() or path.stat().st_mode & 0o777 != 0o600:
        raise ValueError(f"missing or non-private report: {path}")
    return json.loads(path.read_text())


def write_private_json(path, value):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(path.parent, 0o700)
    fd, temp = tempfile.mkstemp(prefix=".proposals-v2-", suffix=".json", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w") as output:
            json.dump(value, output, ensure_ascii=False, indent=2)
            output.write("\n")
        os.replace(temp, path)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def main():
    if len(sys.argv) not in (2, 3) or not Path(sys.argv[1]).is_absolute() or (len(sys.argv) == 3 and sys.argv[2] != "--published"):
        raise SystemExit("usage: v2_registry.py /absolute/path/to/reports [--published]")
    root = Path(sys.argv[1]).resolve()
    published = len(sys.argv) == 3
    manifest = load_manifest(root, published)
    source_by_id = {session["session_id"]: session for session in manifest["sessions"]}
    v2_sources = {}
    for path in (root / "v2" / "sources").glob("*.json"):
        source = private_json(path)
        sid = source.get("session_id")
        if not isinstance(sid, str) or path.stem != sid or not isinstance(source.get("source_sha256"), str) or type(source.get("source_records")) is not int or source["source_records"] < 1:
            raise ValueError(f"invalid session descriptor: {path}")
        v2_sources[sid] = source
        if sid not in source_by_id:
            source_by_id[sid] = source
    reports = {}
    paths = (sorted((root / "v2" / "deep").glob("*.json")) if published else
             [root / "rebuild" / session["session_id"] / "deep.v2.json" for session in manifest["sessions"]])
    if not paths:
        raise ValueError("no published v2 reports")
    for path in paths:
        sid = path.stem if published else path.parent.name
        report = private_json(path)
        # A newly approved Deep may use a newer frozen prefix while the v1
        # manifest remains unchanged. Earlier published Deep keeps its old
        # descriptor until the reviewed replacement is promoted.
        session = select_source(source_by_id.get(sid), v2_sources.get(sid) if published else None, report)
        if session is None:
            raise ValueError(f"no frozen source descriptor for {sid}")
        validate_deep(report, session_id=sid, source_sha256=session["source_sha256"], source_records=session["source_records"])
        reports[sid] = (report, session["source_records"])
    previous_path = root / "v2" / "proposals.json"
    previous = private_json(previous_path) if previous_path.is_file() else None
    lifecycle_events = read_events(root) if published else []
    if published and previous:
        for old in previous["proposals"]:
            if (old["decision"] != DEFAULT_DECISION or old["execution"] != DEFAULT_EXECUTION or old["effect"] != DEFAULT_EFFECT):
                if not any(event["proposal_id"] == old["proposal_id"] and event["proposal_fingerprint"] == proposal_fingerprint(old) for event in lifecycle_events):
                    raise ValueError("published proposal has lifecycle state without a journal record")
    if published and previous:
        prior_sources = {source["session_id"] for proposal in previous["proposals"] for source in proposal["sources"]}
        if not prior_sources.issubset(reports):
            raise ValueError("published v2 report set is incomplete; refusing to drop prior proposal evidence")
    registry = merge_candidates(reports, previous, lifecycle_events if published else None)
    validate_registry(registry, reports)
    output_path = previous_path if published else root / "rebuild" / "proposals.v2.json"
    if published and previous_path.is_file():
        history = root / "v2" / "history"
        history.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chmod(history, 0o700)
        backup = history / f"proposals-before-{time.time_ns()}.json"
        shutil.copyfile(previous_path, backup)
        os.chmod(backup, 0o600)
    write_private_json(output_path, registry)
    print(f"validated {len(reports)} sessions and {len(registry['proposals'])} grouped proposals")


if __name__ == "__main__":
    main()
