#!/usr/bin/env python3
"""Record owner decisions, observed applications and measured effects locally.

The journal is append-only and is the source of lifecycle state. This command
never changes the target of a proposal. Input is a private JSON file so the
owner's exact decision and its reference can be reviewed before recording it.
"""

import argparse
import copy
import fcntl
import hashlib
import json
import os
import re
import subprocess
import sys
import uuid
from datetime import datetime, timezone
from pathlib import Path


ZERO_HASH = "0" * 64
DEFAULT_DECISION = {"status": "not_requested", "at": ""}
DEFAULT_EXECUTION = {"status": "not_applied", "at": "", "version": "", "evidence": []}
DEFAULT_EFFECT = {"status": "not_measured", "method": "", "metric": "", "n_before": 0, "n_after": 0, "note": ""}
EVENTS = {"decision", "application", "effect"}
DECISIONS = {"accepted", "rejected", "revision_requested"}
EFFECTS = {"helped", "no_effect", "worse", "insufficient_data"}


def canonical(value):
    return json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def proposal_fingerprint(proposal):
    """Bind a lifecycle event to the exact target and change reviewed."""
    return hashlib.sha256(canonical({"group_key": proposal["group_key"], "target": proposal["target"], "change": proposal["change"]})).hexdigest()


def journal_path(data_dir):
    return Path(data_dir) / "v2" / "lifecycle" / "events.jsonl"


def _nonempty(value, name):
    if not isinstance(value, str) or not value.strip() or len(value) > 1000:
        raise ValueError(f"{name}: expected nonempty text up to 1000 characters")


def _strings(value, name, *, required=False):
    if not isinstance(value, list) or (required and not value):
        raise ValueError(f"{name}: expected {'nonempty ' if required else ''}array")
    for item in value:
        _nonempty(item, name)


def _validate_detail(event, detail):
    if not isinstance(detail, dict):
        raise ValueError("detail: expected object")
    if event == "decision":
        if set(detail) != {"status"} or detail["status"] not in DECISIONS:
            raise ValueError("decision detail needs accepted, rejected or revision_requested")
    elif event == "application":
        if set(detail) != {"version", "target_version_verified", "evidence", "permission_ref", "rule_review"}:
            raise ValueError("application detail needs version, target_version_verified, evidence, permission_ref and rule_review")
        for key in ("version", "target_version_verified", "permission_ref", "rule_review"):
            _nonempty(detail[key], f"detail.{key}")
        _strings(detail["evidence"], "detail.evidence", required=True)
    else:
        if set(detail) != {"status", "method", "metric", "n_before", "n_after", "note", "evidence"} or detail["status"] not in EFFECTS:
            raise ValueError("effect detail has invalid fields or status")
        for key in ("method", "metric", "note"):
            if not isinstance(detail[key], str) or len(detail[key]) > 1000:
                raise ValueError(f"detail.{key}: expected text up to 1000 characters")
        for key in ("n_before", "n_after"):
            if type(detail[key]) is not int or detail[key] < 0:
                raise ValueError(f"detail.{key}: expected nonnegative integer")
        _strings(detail["evidence"], "detail.evidence", required=True)
        if detail["status"] != "insufficient_data":
            for key in ("method", "metric"):
                _nonempty(detail[key], f"detail.{key}")
            if detail["n_before"] == 0 or detail["n_after"] == 0:
                raise ValueError("measured effect needs observations before and after")
        else:
            _nonempty(detail["note"], "detail.note")


def _validate_record(record, previous_hash):
    expected = {"schema_version", "event_id", "recorded_at", "proposal_id", "proposal_fingerprint", "event", "actor", "authority_ref", "proposal_sources", "detail", "previous_hash", "record_hash"}
    if not isinstance(record, dict) or set(record) != expected or record["schema_version"] != 1:
        raise ValueError("invalid lifecycle journal record")
    for key in ("event_id", "recorded_at", "proposal_id", "actor", "authority_ref"):
        _nonempty(record[key], key)
    if record["event"] not in EVENTS or record["previous_hash"] != previous_hash:
        raise ValueError("invalid lifecycle event or broken hash chain")
    if not isinstance(record["proposal_fingerprint"], str) or len(record["proposal_fingerprint"]) != 64:
        raise ValueError("invalid proposal fingerprint")
    if not isinstance(record["proposal_sources"], list) or not record["proposal_sources"]:
        raise ValueError("lifecycle event needs frozen proposal sources")
    for source in record["proposal_sources"]:
        if not isinstance(source, dict) or set(source) != {"session_id", "task_id", "source_sha256", "evidence"}:
            raise ValueError("invalid lifecycle proposal source")
        _nonempty(source["session_id"], "proposal_sources.session_id")
        _nonempty(source["task_id"], "proposal_sources.task_id")
        if not isinstance(source["source_sha256"], str) or not re.fullmatch(r"[0-9a-f]{64}", source["source_sha256"]):
            raise ValueError("invalid lifecycle source version")
        _strings(source["evidence"], "proposal_sources.evidence", required=True)
        if any(not re.fullmatch(r"L[1-9][0-9]*", line) for line in source["evidence"]):
            raise ValueError("invalid lifecycle source line")
    _validate_detail(record["event"], record["detail"])
    actual_hash = hashlib.sha256(canonical({key: value for key, value in record.items() if key != "record_hash"})).hexdigest()
    if record["record_hash"] != actual_hash:
        raise ValueError("lifecycle journal hash mismatch")


def _read_locked(handle):
    handle.seek(0)
    events = []
    previous_hash = ZERO_HASH
    seen_ids = set()
    for line_number, line in enumerate(handle, 1):
        if not line.endswith("\n"):
            raise ValueError(f"incomplete lifecycle journal line {line_number}")
        record = json.loads(line)
        _validate_record(record, previous_hash)
        if record["event_id"] in seen_ids:
            raise ValueError("duplicate lifecycle event id")
        seen_ids.add(record["event_id"])
        events.append(record)
        previous_hash = record["record_hash"]
    return events


def read_events(data_dir):
    path = journal_path(data_dir)
    if not path.exists():
        return []
    if path.stat().st_mode & 0o777 != 0o600:
        raise ValueError("lifecycle journal must have 0600 permissions")
    with path.open("r", encoding="utf-8") as handle:
        fcntl.flock(handle.fileno(), fcntl.LOCK_SH)
        try:
            return _read_locked(handle)
        finally:
            fcntl.flock(handle.fileno(), fcntl.LOCK_UN)


def _apply_state(proposal, event):
    kind, detail, when = event["event"], event["detail"], event["recorded_at"]
    if kind == "decision":
        if proposal["execution"]["status"] == "applied":
            raise ValueError("cannot change decision after application")
        proposal["decision"] = {"status": detail["status"], "at": when}
    elif kind == "application":
        if proposal["readiness"]["status"] != "prepared_verified" or proposal["decision"]["status"] != "accepted":
            raise ValueError("application requires prepared proposal and accepted decision")
        if proposal["execution"]["status"] == "applied":
            raise ValueError("application already recorded for this proposal version")
        if detail["target_version_verified"] != proposal["target"]["version"]:
            raise ValueError("target version changed; prepare a new proposal")
        proposal["execution"] = {"status": "applied", "at": when, "version": detail["version"], "evidence": copy.deepcopy(detail["evidence"])}
    else:
        if proposal["execution"]["status"] != "applied":
            raise ValueError("effect cannot precede application")
        proposal["effect"] = {key: copy.deepcopy(detail[key]) for key in ("status", "method", "metric", "n_before", "n_after", "note")}


def project_proposal(proposal, events):
    """Replay matching records; retain older versions only as journal history."""
    result = copy.deepcopy(proposal)
    result["decision"] = copy.deepcopy(DEFAULT_DECISION)
    result["execution"] = copy.deepcopy(DEFAULT_EXECUTION)
    result["effect"] = copy.deepcopy(DEFAULT_EFFECT)
    fingerprint = proposal_fingerprint(result)
    prior_version = False
    matched = False
    for event in events:
        if event["proposal_id"] != result["proposal_id"]:
            continue
        if event["proposal_fingerprint"] != fingerprint:
            prior_version = True
            continue
        matched = True
        if event["event"] == "application" and result["readiness"]["status"] != "prepared_verified":
            # Application is a recorded historical fact. The registry schema
            # requires its original prepared state, while the new analysis
            # conflict remains visible for review in history_review.
            result["history_review"] = (result.get("history_review", "") + " New analysis questions readiness after this version was applied; review before another change.").strip()
            result["readiness"] = {"status": "prepared_verified", "reason": "Prepared when this recorded version was applied; new analysis requires review."}
        _apply_state(result, event)
    if prior_version and not matched:
        if result["readiness"]["status"] != "prepared_verified":
            result["readiness"] = {"status": "needs_specification", "reason": "Target or change differs from recorded lifecycle; inspect current state."}
        result["history_review"] = (result.get("history_review", "") + " Earlier decisions and applications remain in the private lifecycle journal; a new owner decision is required.").strip()
    elif prior_version:
        result["history_review"] = (result.get("history_review", "") + " Earlier versions remain in the private lifecycle journal.").strip()
    return result


def record_event(data_dir, request):
    if not isinstance(request, dict) or set(request) != {"proposal_id", "event", "actor", "authority_ref", "detail"}:
        raise ValueError("request needs proposal_id, event, actor, authority_ref and detail")
    for key in ("proposal_id", "actor", "authority_ref"):
        _nonempty(request[key], key)
    if request["event"] not in EVENTS:
        raise ValueError("invalid lifecycle event")
    _validate_detail(request["event"], request["detail"])
    data_dir = Path(data_dir)
    registry_path = data_dir / "v2" / "proposals.json"
    if not registry_path.is_file() or registry_path.stat().st_mode & 0o777 != 0o600:
        raise ValueError("published private proposal registry is required")
    path = journal_path(data_dir)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(path.parent, 0o700)
    descriptor = os.open(path, os.O_CREAT | os.O_RDWR, 0o600)
    with os.fdopen(descriptor, "r+", encoding="utf-8") as handle:
        if os.fstat(handle.fileno()).st_mode & 0o777 != 0o600:
            raise ValueError("lifecycle journal must have 0600 permissions")
        fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
        try:
            events = _read_locked(handle)
            registry = json.loads(registry_path.read_text(encoding="utf-8"))
            matches = [item for item in registry.get("proposals", []) if item.get("proposal_id") == request["proposal_id"]]
            if len(matches) != 1:
                raise ValueError("proposal id is not in the published registry")
            proposal = project_proposal(matches[0], events)
            if request["event"] == "application" and proposal["readiness"]["status"] != "prepared_verified":
                raise ValueError("proposal is not prepared for application")
            record = {"schema_version": 1, "event_id": str(uuid.uuid4()),
                      "recorded_at": datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z"),
                      "proposal_id": request["proposal_id"], "proposal_fingerprint": proposal_fingerprint(proposal),
                      "event": request["event"], "actor": request["actor"], "authority_ref": request["authority_ref"],
                      "proposal_sources": copy.deepcopy(proposal["sources"]), "detail": copy.deepcopy(request["detail"]),
                      "previous_hash": events[-1]["record_hash"] if events else ZERO_HASH}
            _apply_state(proposal, record)  # reject invalid transition before appending
            record["record_hash"] = hashlib.sha256(canonical(record)).hexdigest()
            handle.seek(0, os.SEEK_END)
            handle.write(json.dumps(record, ensure_ascii=False, separators=(",", ":")) + "\n")
            handle.flush()
            os.fsync(handle.fileno())
        finally:
            fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
    return record


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("data_dir", type=Path, help="absolute private reports directory")
    parser.add_argument("request_file", type=Path, help="private 0600 JSON describing one explicit lifecycle event")
    args = parser.parse_args()
    if not args.data_dir.is_absolute() or not args.request_file.is_absolute():
        parser.error("both paths must be absolute")
    if args.request_file.stat().st_mode & 0o777 != 0o600:
        parser.error("request file must have 0600 permissions")
    request = json.loads(args.request_file.read_text(encoding="utf-8"))
    record = record_event(args.data_dir, request)
    # A committed journal entry remains authoritative if refreshing the view fails.
    command = [sys.executable, str(Path(__file__).with_name("v2_registry.py")), str(args.data_dir), "--published"]
    refreshed = subprocess.run(command, capture_output=True, text=True, check=False)
    if refreshed.returncode:
        raise SystemExit("Lifecycle event recorded, but registry refresh failed; rerun v2_registry.py --published")
    print(f"recorded {record['event']} {record['event_id']}")


if __name__ == "__main__":
    main()
