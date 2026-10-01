import hashlib
import json
import stat
import tempfile
import unittest
from pathlib import Path

from v2_review_index import build_index, redact_preview


class ReviewIndexTest(unittest.TestCase):
    def test_sensitive_preview_is_redacted(self):
        sample = "пароль: example-secret-123 and API key=sk-abcdefghijklmnopqrstuv email alex@example.org token " + "YWJj" * 20 + "== bare Abcdefghijklmnop12345 apify_api_" + "a" * 36
        preview, redacted = redact_preview(sample)
        self.assertTrue(redacted)
        self.assertNotIn("example-secret-123", preview)
        self.assertNotIn("sk-abcdefghijklmnopqrstuv", preview)
        self.assertNotIn("alex@example.org", preview)
        self.assertNotIn("YWJj" * 20, preview)
        self.assertNotIn("Abcdefghijklmnop12345", preview)
        self.assertNotIn("apify_api_" + "a" * 36, preview)

    def test_markdown_password_label_redacts_entire_line(self):
        value = "Before\n- **Пароль:** `short-value`\nAfter"
        preview, redacted = redact_preview(value)
        self.assertTrue(redacted)
        self.assertNotIn("short-value", preview)
        self.assertNotIn("Пароль", preview)
        self.assertIn("Before", preview)
        self.assertIn("After", preview)

    def test_frozen_prefix_and_line_references(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            source = root / "source.jsonl"
            records = [
                {"type": "response_item", "timestamp": "2026-09-30T01:00:00Z", "payload": {
                    "type": "message", "role": "user", "content": [{"type": "input_text", "text": "one"}]}},
                {"type": "response_item", "timestamp": "2026-09-30T01:01:00Z", "payload": {
                    "type": "function_call", "name": "check", "call_id": "c1", "arguments": "x" * 400}},
                {"type": "response_item", "timestamp": "2026-09-30T01:02:00Z", "payload": {
                    "type": "message", "role": "assistant", "phase": "final_answer",
                    "content": [{"type": "output_text", "text": "done"}]}},
                {"type": "response_item", "timestamp": "2026-09-30T01:03:00Z", "payload": {
                    "type": "message", "role": "user", "content": [{"type": "input_text", "text": "later"}]}},
            ]
            raw = [json.dumps(item).encode() + b"\n" for item in records]
            source.write_bytes(b"".join(raw))
            session = {"session_id": "test", "source_path": str(source),
                       "source_records": 3, "source_sha256": hashlib.sha256(b"".join(raw[:3])).hexdigest()}
            out = root / "private"
            self.assertEqual(build_index(session, out), 3)
            lines = [json.loads(line) for line in (out / "test.jsonl").read_text().splitlines()]
            self.assertEqual([item["line"] for item in lines], [1, 2, 3])
            self.assertEqual(lines[0]["text"], "one")
            self.assertTrue(lines[1]["truncated"])
            self.assertEqual(lines[2]["category"], "assistant_final_answer")
            self.assertEqual(stat.S_IMODE((out / "test.jsonl").stat().st_mode), 0o600)
            session["source_sha256"] = "wrong"
            with self.assertRaises(ValueError):
                build_index(session, out)
            self.assertEqual([json.loads(line)["line"] for line in
                              (out / "test.jsonl").read_text().splitlines()], [1, 2, 3])


if __name__ == "__main__":
    unittest.main()
