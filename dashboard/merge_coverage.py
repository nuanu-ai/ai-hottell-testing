#!/usr/bin/env python3
"""Combine private agent reviews into complete 13-check local reports.

Usage: python3 dashboard/merge_coverage.py /absolute/path/to/local-data/reports
No session content is printed or sent to another process.
"""

import hashlib
import json
import os
import re
import sys
import tempfile
from pathlib import Path

LOCAL = {"D01", "D03", "D05", "D07", "D12", "D16", "D24"}
CROSS = {"D10", "D13", "D19", "D22", "D25", "D26"}
ORDER = ["D01", "D03", "D05", "D07", "D10", "D12", "D13", "D16", "D19", "D22", "D24", "D25", "D26"]
STATUSES = {"suspected", "checked_clear", "insufficient_data", "not_applicable", "not_checked"}


def load_part(path: Path, session: dict, expected: set[str]) -> dict[str, dict]:
    data = json.loads(path.read_text())
    if (data.get("kind"), data.get("schema_version"), data.get("session_id"), data.get("source_sha256")) != (
        "detector_coverage_part", 1, session["session_id"], session["source_sha256"]
    ):
        raise ValueError(f"invalid identity in {path}")
    checks = data.get("checks", [])
    if len(checks) != len(expected) or {c.get("id") for c in checks} != expected:
        raise ValueError(f"missing, duplicate or extra detector check in {path}")
    for check in checks:
        if check.get("status") not in STATUSES or not check.get("summary"):
            raise ValueError(f"invalid status or summary for {check.get('id')} in {path}")
        if check["status"] in {"suspected", "checked_clear"} and not check.get("evidence"):
            raise ValueError(f"missing evidence for {check['id']} in {path}")
        if check["status"] == "insufficient_data" and not check.get("missing_data"):
            raise ValueError(f"missing-data reason required for {check['id']} in {path}")
        if len(check["summary"]) > 500 or any(len(e) > 300 for e in check.get("evidence", [])):
            raise ValueError(f"oversized output for {check['id']} in {path}")
        if expected == LOCAL:
            for evidence in check.get("evidence", []):
                refs = [int(n) for pair in re.findall(r"\bL(\d+)(?:[–-]L?(\d+))?", evidence) for n in pair if n]
                if (check["status"] in {"suspected", "checked_clear"} and not refs) or any(n < 1 or n > session["source_records"] for n in refs):
                    raise ValueError(f"invalid source-line evidence for {check['id']} in {path}")
    return {check["id"]: check for check in checks}


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: merge_coverage.py /absolute/path/to/local-data/reports")
    root = Path(sys.argv[1])
    if not root.is_absolute():
        raise ValueError("private report directory must be absolute")
    root = root.resolve()
    manifest = json.loads((root / "telemetry" / "manifest.json").read_text())
    sessions = manifest["sessions"]
    if len(sessions) != len({s["session_id"] for s in sessions}):
        raise ValueError("duplicate session in manifest")

    reports = []
    for session in sessions:
        sid = session["session_id"]
        digest = hashlib.sha256()
        with open(session["source_path"], "rb") as source:
            for _ in range(session["source_records"]):
                line = source.readline()
                if not line:
                    raise ValueError(f"source prefix shorter than manifest for {sid}")
                digest.update(line)
        if digest.hexdigest() != session["source_sha256"]:
            raise ValueError(f"source prefix changed since manifest for {sid}")
        local_paths = [root / "coverage" / "parts" / name / f"{sid}.json" for name in ("local-a", "local-b")]
        existing = [path for path in local_paths if path.is_file()]
        if len(existing) != 1:
            raise ValueError(f"expected exactly one local review for {sid}")
        local = load_part(existing[0], session, LOCAL)
        cross = load_part(root / "coverage" / "parts" / "cross" / f"{sid}.json", session, CROSS)
        checks = local | cross
        reports.append({
            "kind": "detector_coverage", "schema_version": 1, "catalogue_version": "0.1",
            "session_id": sid, "source_sha256": session["source_sha256"],
            "checks": [checks[detector] for detector in ORDER],
        })

    output = root / "coverage"
    output.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(output, 0o700)
    for report in reports:
        fd, temp = tempfile.mkstemp(prefix=".coverage-", suffix=".json", dir=output)
        try:
            os.fchmod(fd, 0o600)
            with os.fdopen(fd, "w") as handle:
                json.dump(report, handle, ensure_ascii=False, indent=2)
                handle.write("\n")
            os.replace(temp, output / (report["session_id"] + ".json"))
        finally:
            if os.path.exists(temp):
                os.unlink(temp)
    print(f"merged {len(reports)} private reports; {len(ORDER)} checks each")


if __name__ == "__main__":
    main()
