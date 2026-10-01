"""Publication gate tests with synthetic source and model report."""

import hashlib
import json
import tempfile
import unittest
from pathlib import Path

from local import v2_publish_new, v2_worker
from local.test_v2_worker import SESSION, report, sample_source
from dashboard.rebuild_telemetry import rebuild


class PublishNewTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.data = self.root / "reports"
        manifest = self.data / "telemetry" / "manifest.json"
        manifest.parent.mkdir(parents=True, exist_ok=True)
        manifest.write_text('{"sessions":[]}')
        self.source = self.root / f"rollout-{SESSION}.jsonl"
        sha = sample_source(self.source)
        descriptor = {"session_id": SESSION, "source_path": str(self.source), "source_sha256": sha, "source_records": 2}
        v2_worker.atomic_json(self.data / "v2" / "sources" / f"{SESSION}.json", descriptor)
        self.candidate_path = self.data / "v2" / "review" / f"{SESSION}.json"
        candidate = report(sha)
        for check in candidate["checks"]:
            check["status"] = "not_applicable"
        v2_worker.atomic_json(self.candidate_path, candidate)
        rebuild(descriptor, self.data / "v2" / "review-telemetry", retrospective=False)
        self.review_path = self.root / "review.json"

    def review(self, **overrides):
        value = {
            "session_id": SESSION,
            "candidate_sha256": hashlib.sha256(self.candidate_path.read_bytes()).hexdigest(),
            "verdict": "approved",
            "reviewer": "independent-agent",
            "reviewed_at": "2026-09-30T00:00:00Z",
            "note": "Inspected original two lines and all checks; no sensitive content.",
            "checks": sorted(v2_publish_new.REVIEW_CHECKS),
        }
        value.update(overrides)
        self.review_path.write_text(json.dumps(value))
        self.review_path.chmod(0o600)

    def stage_refresh(self):
        with self.source.open("a") as output:
            output.write(json.dumps({"type": "event_msg", "payload": {"type": "task_complete"}}) + "\n")
        descriptor = v2_worker.source_prefix(SESSION, self.source)
        v2_worker.atomic_json(self.data / "v2" / "review-sources" / f"{SESSION}.json", descriptor)
        candidate = report(descriptor["source_sha256"])
        for check in candidate["checks"]:
            check["status"] = "not_applicable"
        v2_worker.atomic_json(self.candidate_path, candidate)
        rebuild(descriptor, self.data / "v2" / "review-telemetry", retrospective=False)
        self.review()
        return descriptor

    def test_exact_review_required_and_publication_updates_registry(self):
        self.review(candidate_sha256="0" * 64)
        with self.assertRaises(ValueError):
            v2_publish_new.publish(self.data, SESSION, self.review_path)
        self.assertFalse((self.data / "v2" / "deep" / f"{SESSION}.json").exists())
        self.review()
        published = v2_publish_new.publish(self.data, SESSION, self.review_path)
        self.assertTrue(published.is_file())
        self.assertEqual(published.stat().st_mode & 0o777, 0o600)
        technical = self.data / "telemetry" / f"{SESSION}.json"
        self.assertTrue(technical.is_file())
        self.assertEqual(json.loads(technical.read_text())["source_coverage"]["hook"]["status"], "separate_store")
        registry = json.loads((self.data / "v2" / "proposals.json").read_text())
        self.assertEqual(registry["proposals"], [])

    def test_failed_registry_refresh_restores_unpublished_state(self):
        self.review()
        def fail(_):
            raise RuntimeError("failed")
        with self.assertRaises(RuntimeError):
            v2_publish_new.publish(self.data, SESSION, self.review_path, fail)
        self.assertFalse((self.data / "v2" / "deep" / f"{SESSION}.json").exists())
        self.assertFalse((self.data / "telemetry" / f"{SESSION}.json").exists())

    def test_source_mutation_prevents_publication(self):
        self.review()
        self.source.write_text(self.source.read_text().replace("Create a draft", "Create a plan"))
        with self.assertRaises(ValueError):
            v2_publish_new.publish(self.data, SESSION, self.review_path)

    def test_unfinished_check_cannot_be_approved(self):
        candidate = json.loads(self.candidate_path.read_text())
        candidate["checks"][0]["status"] = "not_checked"
        v2_worker.atomic_json(self.candidate_path, candidate)
        self.review()
        with self.assertRaisesRegex(ValueError, "unfinished Deep checks"):
            v2_publish_new.publish(self.data, SESSION, self.review_path)

    def test_refreshed_prefix_needs_exact_review_then_replaces_published_version(self):
        self.review()
        published = v2_publish_new.publish(self.data, SESSION, self.review_path)
        old_report = published.read_bytes()
        old_source = (self.data / "v2" / "sources" / f"{SESSION}.json").read_bytes()
        descriptor = self.stage_refresh()
        self.assertEqual(published.read_bytes(), old_report)
        self.assertEqual((self.data / "v2" / "sources" / f"{SESSION}.json").read_bytes(), old_source)
        wrong_review = json.loads(self.review_path.read_text())
        wrong_review["candidate_sha256"] = hashlib.sha256(old_report).hexdigest()
        self.review_path.write_text(json.dumps(wrong_review))
        with self.assertRaises(ValueError):
            v2_publish_new.publish(self.data, SESSION, self.review_path)
        self.review()
        v2_publish_new.publish(self.data, SESSION, self.review_path)
        self.assertEqual(json.loads(published.read_text())["source_sha256"], descriptor["source_sha256"])
        self.assertEqual(json.loads((self.data / "v2" / "sources" / f"{SESSION}.json").read_text())["source_sha256"], descriptor["source_sha256"])
        self.assertEqual(json.loads((self.data / "telemetry" / f"{SESSION}.json").read_text())["source"]["sha256"], descriptor["source_sha256"])
        history = self.data / "v2" / "history" / SESSION
        self.assertTrue(list(history.glob("deep-*.json")))
        self.assertTrue(list(history.glob("source-*.json")))

    def test_failed_refreshed_registry_restores_old_published_source_and_reports(self):
        self.review()
        v2_publish_new.publish(self.data, SESSION, self.review_path)
        paths = [self.data / "v2" / "deep" / f"{SESSION}.json",
                 self.data / "telemetry" / f"{SESSION}.json",
                 self.data / "v2" / "sources" / f"{SESSION}.json",
                 self.data / "v2" / "proposals.json"]
        before = [path.read_bytes() for path in paths]
        self.stage_refresh()
        def fail(data_dir):
            v2_worker.atomic_json(data_dir / "v2" / "proposals.json", {"damaged": True})
            raise RuntimeError("registry refresh failed")
        with self.assertRaises(RuntimeError):
            v2_publish_new.publish(self.data, SESSION, self.review_path, fail)
        self.assertEqual([path.read_bytes() for path in paths], before)


if __name__ == "__main__":
    unittest.main()
