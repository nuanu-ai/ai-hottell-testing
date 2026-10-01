"""Synthetic gate for publishing a complete historical Deep 2.0 set."""

import hashlib
import json
import tempfile
import unittest
from pathlib import Path

from analytics.test_v2_contract import report_example
from analytics.v2_contract import REVIEW_CHECKS
from promote_rebuild import validate_and_promote_v2


def write_private(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value))
    path.chmod(0o600)


class V2PromotionTest(unittest.TestCase):
    def test_unfinished_checks_and_missing_review_block_publication(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            sid = "session-demo"
            source = root / "source.jsonl"
            records = [{"type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "text", "text": "Create an artifact"}]}}]
            records.extend({"type": "event_msg", "payload": {"type": "task_progress"}} for _ in range(11))
            raw = b"".join((json.dumps(item) + "\n").encode() for item in records)
            source.write_bytes(raw)
            sha = hashlib.sha256(raw).hexdigest()
            session = {"session_id": sid, "source_path": str(source), "source_sha256": sha, "source_records": 12}
            report = report_example()
            report["source_sha256"] = sha
            candidate = root / "rebuild" / sid / "deep.v2.json"
            write_private(candidate, report)
            technical = root / "telemetry" / f"{sid}.json"
            write_private(technical, {"kind": "telemetry", "session_id": sid, "thread_id": sid,
                                      "source": {"sha256": sha, "records": 12},
                                      "source_coverage": {key: {"status": "missing"} for key in ("hook", "otel", "transcript", "app")},
                                      "events": []})
            write_private(root / "rebuild" / "proposals.v2.json", {"kind": "proposal_registry", "schema_version": 2, "proposals": []})
            with self.assertRaisesRegex(ValueError, "unfinished v2 checks"):
                validate_and_promote_v2(root, [session], promote=True)
            for check in report["checks"]:
                check["status"] = "not_applicable"
            write_private(candidate, report)
            with self.assertRaisesRegex(ValueError, "missing or non-private candidate"):
                validate_and_promote_v2(root, [session], promote=True)
            review = {"session_id": sid, "candidate_sha256": hashlib.sha256(candidate.read_bytes()).hexdigest(),
                      "verdict": "approved", "reviewer": "independent-agent", "reviewed_at": "2026-09-30T00:00:00Z",
                      "note": "Read the synthetic source, checked boundaries and classifications.", "checks": sorted(REVIEW_CHECKS)}
            write_private(root / "rebuild" / "reviews" / f"{sid}.json", review)
            technical.unlink()
            with self.assertRaisesRegex(ValueError, "missing or non-private candidate"):
                validate_and_promote_v2(root, [session], promote=True)
            write_private(technical, {"kind": "telemetry", "session_id": sid, "thread_id": sid,
                                      "source": {"sha256": sha, "records": 12},
                                      "source_coverage": {key: {"status": "missing"} for key in ("hook", "otel", "transcript", "app")},
                                      "events": []})
            validate_and_promote_v2(root, [session], promote=True)
            self.assertTrue((root / "v2" / "deep" / f"{sid}.json").is_file())


if __name__ == "__main__":
    unittest.main()
