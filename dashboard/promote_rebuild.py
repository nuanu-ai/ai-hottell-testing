#!/usr/bin/env python3
"""Validate all private report candidates, then promote them together.

Usage: python3 dashboard/promote_rebuild.py /absolute/path/to/local-data/reports [--promote]
Without --promote this is read-only. Keep a private backup before promotion.
"""

import hashlib
import json
import os
import re
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path

# The dashboard script is runnable from the repository root and from its own
# directory. Import the shared v2 contract without requiring an installation.
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from analytics.v2_contract import validate_registry, validate_semantic_review, validate_v2_deep  # noqa: E402
from local.v2_source_validation import validate_source_roles  # noqa: E402


ORDER = ["D01", "D03", "D05", "D07", "D10", "D12", "D13", "D16", "D19", "D22", "D24", "D25", "D26"]
STATUSES = {"suspected", "checked_clear", "insufficient_data", "not_applicable", "not_checked"}
OUTCOMES = {"verified", "partial", "failed", "unknown"}
SAFE_ID = re.compile(r"^[A-Za-z0-9._-]{1,128}$")
SENSITIVE_PATTERNS = (
    re.compile(r"\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b", re.IGNORECASE),
    re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
    re.compile(r"\b(?:sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16})\b"),
)


def read_json(path):
    if not path.is_file() or path.stat().st_mode & 0o777 != 0o600:
        raise ValueError(f"missing or non-private candidate: {path}")
    content = path.read_text()
    if path.name != "telemetry.json" and any(pattern.search(content) for pattern in SENSITIVE_PATTERNS):
        raise ValueError(f"candidate may contain private contact or credential material: {path}")
    return json.loads(content)


def require_local_line_refs(evidence, sid, source_records, where):
    if not isinstance(evidence, list) or not evidence:
        raise ValueError(f"missing evidence: {where}")
    found_local = False
    for item in evidence:
        if not isinstance(item, str) or len(item) > 300:
            raise ValueError(f"invalid evidence: {where}")
        # A report may cite another session too; only local references use this bound.
        for match in re.finditer(r"(?<!:)(?:(?P<ref>[0-9a-f]{8}-[0-9a-f-]{27,}|local_transcript):)?L(?P<line>\d+)\b", item):
            if match.group("ref") not in (None, sid, "local_transcript"):
                continue
            number = int(match.group("line"))
            if 1 <= number <= source_records:
                found_local = True
            else:
                raise ValueError(f"source line outside frozen prefix: {where}")
    if not found_local:
        raise ValueError(f"no local line reference: {where}")


def validate_deep(report, session):
    sid = session["session_id"]
    if set(report) != {"kind", "session_id", "task", "outcome", "outcome_basis", "model_opinion", "observations", "recommendations", "unknowns"}:
        raise ValueError(f"invalid Deep fields: {sid}")
    if report["kind"] != "deep" or report["session_id"] != sid or report["outcome"] not in OUTCOMES:
        raise ValueError(f"invalid Deep identity or outcome: {sid}")
    if not report["task"] or not report["outcome_basis"] or not isinstance(report["unknowns"], list):
        raise ValueError(f"incomplete Deep report: {sid}")
    for number, item in enumerate(report["observations"]):
        if set(item) != {"pattern", "finding", "status", "evidence"} or item["status"] not in {"suspected", "confirmed", "dismissed"} or not item["finding"]:
            raise ValueError(f"invalid Deep observation: {sid}:{number}")
        require_local_line_refs(item["evidence"], sid, session["source_records"], f"{sid}:observation:{number}")
    for number, item in enumerate(report["recommendations"]):
        if set(item) != {"action", "target", "change", "evidence", "status"} or item["status"] != "draft" or not all(item[k] for k in ("action", "target", "change")):
            raise ValueError(f"invalid Deep recommendation: {sid}:{number}")
        require_local_line_refs(item["evidence"], sid, session["source_records"], f"{sid}:recommendation:{number}")


def validate_coverage(report, session):
    sid = session["session_id"]
    if set(report) != {"kind", "schema_version", "catalogue_version", "session_id", "source_sha256", "checks"}:
        raise ValueError(f"invalid coverage fields: {sid}")
    if (report["kind"], report["schema_version"], report["catalogue_version"], report["session_id"], report["source_sha256"]) != (
        "detector_coverage", 1, "0.1", sid, session["source_sha256"]
    ) or [item.get("id") for item in report["checks"]] != ORDER:
        raise ValueError(f"incomplete or stale detector coverage: {sid}")
    for item in report["checks"]:
        status = item.get("status")
        if status not in STATUSES or not item.get("summary") or len(item["summary"]) > 500:
            raise ValueError(f"invalid detector status or summary: {sid}:{item.get('id')}")
        if status in {"suspected", "checked_clear"}:
            require_local_line_refs(item.get("evidence"), sid, session["source_records"], f"{sid}:{item['id']}")
        if status == "insufficient_data" and not item.get("missing_data"):
            raise ValueError(f"missing input reason: {sid}:{item['id']}")


def validate_telemetry(report, session):
    sid = session["session_id"]
    if report.get("kind") != "telemetry" or report.get("session_id") != sid or report.get("thread_id") != sid:
        raise ValueError(f"invalid technical report identity: {sid}")
    if report.get("source", {}).get("sha256") != session["source_sha256"] or report["source"].get("records") != session["source_records"]:
        raise ValueError(f"technical report source mismatch: {sid}")
    if set(report.get("source_coverage", {})) != {"hook", "otel", "transcript", "app"}:
        raise ValueError(f"technical source coverage incomplete: {sid}")
    for event in report.get("events", []):
        if event.get("source") != "transcript" or event.get("provenance") != "reconstructed":
            raise ValueError(f"technical event misattributed: {sid}")
        number = (event.get("evidence") or {}).get("line")
        if not isinstance(number, int) or not 1 <= number <= session["source_records"]:
            raise ValueError(f"technical event outside frozen prefix: {sid}")


def verify_frozen_source(session):
    sid = session["session_id"]
    digest = hashlib.sha256()
    with open(session["source_path"], "rb") as source:
        for _ in range(session["source_records"]):
            line = source.readline()
            if not line:
                raise ValueError(f"source shorter than frozen prefix: {sid}")
            digest.update(line)
    if digest.hexdigest() != session["source_sha256"]:
        raise ValueError(f"source prefix changed: {sid}")


def _private_write(path, content):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(prefix=".promote-", suffix=".json", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "wb") as out:
            out.write(content)
        os.replace(temp, path)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def validate_and_promote_v2(root, sessions, *, promote=False):
    """Validate all v2 candidates; keep prior versions before local promotion."""
    reports = {}
    candidates = []
    for session in sessions:
        verify_frozen_source(session)
        sid = session["session_id"]
        if not SAFE_ID.fullmatch(sid) or sid in {".", ".."}:
            raise ValueError(f"invalid session id in manifest: {sid}")
        candidate = root / "rebuild" / sid / "deep.v2.json"
        report = read_json(candidate)
        validate_v2_deep(report, session)
        validate_source_roles(report, session)
        # Historical technical reports are already published separately. Keep
        # them as a required, source-matched sibling at the Deep 2.0 gate.
        validate_telemetry(read_json(root / "telemetry" / f"{sid}.json"), session)
        if promote:
            unfinished = [check["id"] for check in report["checks"] if check["status"] == "not_checked"]
            if unfinished:
                raise ValueError(f"unfinished v2 checks for {sid}: {', '.join(unfinished)}")
            review = read_json(root / "rebuild" / "reviews" / f"{sid}.json")
            validate_semantic_review(review, session_id=sid, candidate_sha256=hashlib.sha256(candidate.read_bytes()).hexdigest())
        reports[sid] = (report, session["source_records"])
        candidates.append((candidate, root / "v2" / "deep" / f"{sid}.json"))
    registry_source = root / "rebuild" / "proposals.v2.json"
    registry = read_json(registry_source)
    validate_registry(registry, reports)
    candidates.append((registry_source, root / "v2" / "proposals.json"))
    print(f"validated {len(reports)} v2 Deep reports and proposal registry")
    if not promote:
        return
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    # Read all candidate bytes before changing any published file.
    contents = [(source.read_bytes(), destination) for source, destination in candidates]
    for content, destination in contents:
        if destination.exists():
            previous = destination.read_bytes()
            previous_sha = hashlib.sha256(previous).hexdigest()[:16]
            history = root / "v2" / "history" / stamp / f"{destination.stem}-{previous_sha}.json"
            _private_write(history, previous)
        _private_write(destination, content)
    print(f"promoted {len(reports)} v2 Deep reports and registry; previous versions saved in v2/history")


def main():
    if len(sys.argv) not in (2, 3) or not Path(sys.argv[1]).is_absolute() or (len(sys.argv) == 3 and sys.argv[2] not in {"--promote", "--validate-v2", "--promote-v2"}):
        raise SystemExit("usage: promote_rebuild.py /absolute/path/to/local-data/reports [--promote|--validate-v2|--promote-v2]")
    root = Path(sys.argv[1]).resolve()
    sessions = json.loads((root / "telemetry" / "manifest.json").read_text())["sessions"]
    if len(sessions) != len({s["session_id"] for s in sessions}):
        raise ValueError("duplicate session in manifest")
    if len(sys.argv) == 3 and sys.argv[2] in {"--validate-v2", "--promote-v2"}:
        validate_and_promote_v2(root, sessions, promote=sys.argv[2] == "--promote-v2")
        return
    candidates = []
    for session in sessions:
        sid = session["session_id"]
        verify_frozen_source(session)
        folder = root / "rebuild" / sid
        deep = read_json(folder / "deep.json")
        coverage = read_json(folder / "coverage.json")
        telemetry = read_json(folder / "telemetry.json")
        notes = read_json(folder / "technical_notes.json")
        if notes.get("session_id") != sid:
            raise ValueError(f"technical notes have wrong identity: {sid}")
        validate_deep(deep, session)
        validate_coverage(coverage, session)
        validate_telemetry(telemetry, session)
        candidates.extend(((folder / "deep.json", root / "deep" / f"{sid}.json"),
                           (folder / "coverage.json", root / "coverage" / f"{sid}.json"),
                           (folder / "telemetry.json", root / "telemetry" / f"{sid}.json")))
    print(f"validated {len(sessions)} sessions and {len(candidates)} private report candidates")
    if len(sys.argv) == 2:
        return
    for source, destination in candidates:
        fd, temp = tempfile.mkstemp(prefix=".promote-", suffix=".json", dir=destination.parent)
        try:
            os.fchmod(fd, 0o600)
            with os.fdopen(fd, "wb") as out:
                out.write(source.read_bytes())
            os.replace(temp, destination)
        finally:
            if os.path.exists(temp):
                os.unlink(temp)
    print("promoted all validated report candidates")


if __name__ == "__main__":
    main()
