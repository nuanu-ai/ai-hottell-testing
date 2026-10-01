"""Synthetic local queue tests: no model calls and no private session fixtures."""

import hashlib
import json
import os
import tempfile
import unittest
from pathlib import Path

from local import v2_worker
from local import v2_lifecycle
from analytics.v2_contract import CHECK_IDS
from analytics.test_v2_contract import proposal_example


SESSION = "11111111-2222-3333-4444-555555555555"


def sample_source(path):
    records = [
        {"type": "response_item", "timestamp": "2026-01-01T00:00:00Z", "payload": {"type": "message", "role": "user", "content": [{"type": "text", "text": "Create a draft"}]}},
        {"type": "response_item", "timestamp": "2026-01-01T00:00:01Z", "payload": {"type": "message", "role": "assistant", "phase": "final", "content": [{"type": "text", "text": "Draft ready for review"}]}},
    ]
    raw = b"".join((json.dumps(item) + "\n").encode() for item in records)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(raw)
    return hashlib.sha256(raw).hexdigest()


def report(sha):
    return {
        "kind": "deep", "schema_version": 2, "session_id": SESSION, "source_sha256": sha,
        "tasks": [{"task_id": "t1", "goal": "Create a draft", "goal_evidence": ["L1"], "scope": "draft", "start_line": 1, "end_line": 2, "boundary_evidence": ["L1"], "success_criteria": [], "outcome": "unknown", "outcome_basis": ["L2"], "claims": []}],
        "checks": [{"id": code, "status": "not_checked", "summary": "Requires later semantic review", "evidence": [], "missing_data": [], "task_ids": []} for code in CHECK_IDS],
        "observations": [], "proposal_candidates": [], "unknowns": ["Result has not been independently verified"],
    }


class WorkerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.data = self.root / "reports"
        self.sessions = self.root / "sessions"
        self.requests = self.data / "v2" / "requests"
        self.requests.mkdir(parents=True)
        manifest = self.data / "telemetry" / "manifest.json"
        manifest.parent.mkdir(parents=True, exist_ok=True)
        manifest.write_text(json.dumps({"sessions": []}))
        self.source = self.sessions / "2026" / "01" / "01" / f"rollout-{SESSION}.jsonl"
        self.sha = sample_source(self.source)

    def enqueue(self, **more):
        payload = {"session_id": SESSION, "status": "pending", "requested_at": "2026-01-01T00:00:00Z", **more}
        target = self.requests / f"{SESSION}.json"
        v2_worker.atomic_json(target, payload)
        return target

    def fake_executor(self, prompt, output_path):
        self.assertIn(str(self.source), prompt)
        self.assertIn("review_index=", prompt)
        self.assertIn("analytics/catalogue.yaml", prompt)
        self.assertIn(f"published_v2_corpus={self.data / 'v2' / 'deep'}", prompt)
        output_path.write_text(json.dumps(report(self.sha)))

    def test_new_session_freezes_source_and_stages_valid_deep(self):
        request = self.enqueue()
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, self.fake_executor), 1)
        state = json.loads(request.read_text())
        self.assertEqual(state["status"], "completed")
        self.assertFalse(state["published"])
        self.assertEqual(state["source_sha256"], self.sha)
        self.assertEqual(json.loads((self.data / "v2" / "sources" / f"{SESSION}.json").read_text())["source_records"], 2)
        self.assertEqual(json.loads((self.data / "v2" / "review" / f"{SESSION}.json").read_text())["schema_version"], 2)
        technical = self.data / "v2" / "review-telemetry" / SESSION / "telemetry.json"
        self.assertTrue(technical.is_file())
        self.assertEqual(json.loads(technical.read_text())["source_coverage"]["hook"]["status"], "separate_store")
        self.assertFalse((self.data / "v2" / "deep" / f"{SESSION}.json").exists())
        self.assertFalse((self.data / "v2" / "proposals.json").exists())
        self.assertEqual(state["proposal_refresh"], "awaiting_review")
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, self.fake_executor), 0)
        for path in [request, self.data / "v2" / "sources" / f"{SESSION}.json", self.data / "v2" / "review" / f"{SESSION}.json", technical]:
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_cross_session_skill_request_stages_candidate_without_publishing(self):
        published = self.data / "v2" / "deep" / f"{SESSION}.json"
        v2_worker.atomic_json(published, report(self.sha))
        request_path = self.data / "v2" / "skill-analysis-request.json"
        v2_worker.atomic_json(request_path, {"status": "pending", "requested_at": "2026-01-01T00:00:00Z"})

        def skill_executor(prompt, output_path):
            self.assertIn("межсессионный анализ", prompt)
            inventory = json.loads((self.data / "v2" / "skill-inventory.json").read_text())
            output_path.write_text(json.dumps({
                "kind": "skill_opportunities", "schema_version": 1, "analyzed_at": "2026-01-01T00:00:00Z",
                "corpus": [{"session_id": SESSION, "source_sha256": self.sha, "deep_report_sha256": hashlib.sha256(published.read_bytes()).hexdigest()}],
                "inventory": inventory, "opportunities": [],
            }))

        self.assertEqual(v2_worker.run_once(self.data, self.sessions, skill_executor), 1)
        self.assertEqual(json.loads(request_path.read_text())["status"], "completed")
        self.assertTrue((self.data / "v2" / "skill-opportunities.candidate.json").is_file())
        self.assertFalse((self.data / "v2" / "skill-opportunities.json").exists())
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, skill_executor), 0)

    def test_manifest_session_uses_frozen_prefix_and_stages_candidate(self):
        manifest = self.data / "telemetry" / "manifest.json"
        manifest.parent.mkdir(parents=True, exist_ok=True)
        manifest.write_text(json.dumps({"sessions": [{"session_id": SESSION, "source_path": str(self.source), "source_sha256": self.sha, "source_records": 2}]}))
        with self.source.open("a") as output:
            output.write(json.dumps({"type": "event_msg", "payload": {"type": "task_complete"}}) + "\n")
        request = self.enqueue(source_sha256=self.sha)
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, self.fake_executor), 1)
        state = json.loads(request.read_text())
        self.assertEqual(state["status"], "completed")
        self.assertFalse(state["published"])
        self.assertTrue((self.data / "rebuild" / SESSION / "deep.v2.json").is_file())
        self.assertFalse((self.data / "v2" / "deep" / f"{SESSION}.json").exists())

    def test_unavailable_source_is_failed_without_executor(self):
        self.source.unlink()
        request = self.enqueue()
        def should_not_run(*_):
            self.fail("model was called without source")
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, should_not_run), 1)
        state = json.loads(request.read_text())
        self.assertEqual((state["status"], state["error_code"]), ("failed", "source_unavailable"))
        self.assertNotIn("result_path", state)

    def test_invalid_model_report_is_not_published(self):
        request = self.enqueue()
        calls = []
        def invalid_executor(_prompt, output_path):
            calls.append(1)
            output_path.write_text('{"kind":"deep"}')
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, invalid_executor), 1)
        state = json.loads(request.read_text())
        self.assertEqual((state["status"], state["error_code"]), ("failed", "invalid_analysis"))
        self.assertEqual(len(calls), 2)
        self.assertFalse((self.data / "v2" / "deep" / f"{SESSION}.json").exists())

    def test_compacted_summary_cannot_define_a_task(self):
        records = [json.loads(line) for line in self.source.read_text().splitlines()]
        records[1] = {"type": "compacted", "payload": {"replacement_history": "Earlier user wanted another task"}}
        raw = b"".join((json.dumps(item) + "\n").encode() for item in records)
        self.source.write_bytes(raw)
        self.sha = hashlib.sha256(raw).hexdigest()
        request = self.enqueue()
        calls = []
        def summary_executor(_prompt, output_path):
            calls.append(1)
            result = report(self.sha)
            result["tasks"][0]["start_line"] = 2
            result["tasks"][0]["goal_evidence"] = ["L2"]
            output_path.write_text(json.dumps(result))
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, summary_executor), 1)
        state = json.loads(request.read_text())
        self.assertEqual((state["status"], state["error_code"]), ("failed", "invalid_analysis"))
        self.assertEqual(len(calls), 2)
        self.assertFalse((self.data / "v2" / "review" / f"{SESSION}.json").exists())

    def test_one_repair_attempt_can_succeed(self):
        request = self.enqueue()
        calls = []
        def repairing_executor(prompt, output_path):
            calls.append(prompt)
            output_path.write_text('{"kind":"deep"}' if len(calls) == 1 else json.dumps(report(self.sha)))
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, repairing_executor), 1)
        self.assertEqual(json.loads(request.read_text())["status"], "completed")
        self.assertEqual(len(calls), 2)
        self.assertIn("структурную проверку", calls[1])

    def test_incomplete_tail_is_excluded_from_new_source_version(self):
        with self.source.open("ab") as source:
            source.write(b'{"type":"event_msg"')
        request = self.enqueue()
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, self.fake_executor), 1)
        state = json.loads(request.read_text())
        self.assertEqual(state["status"], "completed")
        self.assertEqual(state["source_records"], 2)
        self.assertEqual(state["source_sha256"], self.sha)

    def test_requested_source_version_must_match(self):
        request = self.enqueue(source_sha256="0" * 64)
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, self.fake_executor), 1)
        state = json.loads(request.read_text())
        self.assertEqual((state["status"], state["error_code"]), ("failed", "source_version_mismatch"))

    def test_structural_success_does_not_refresh_registry(self):
        request = self.enqueue()
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, self.fake_executor), 1)
        self.assertEqual(json.loads(request.read_text())["status"], "completed")
        self.assertFalse((self.data / "v2" / "proposals.json").exists())

    def publish_fixture(self):
        descriptor = v2_worker.freeze_new_source(SESSION, self.source, self.data)
        v2_worker.atomic_json(self.data / "v2" / "deep" / f"{SESSION}.json", report(self.sha))
        return descriptor

    def test_refresh_same_prefix_does_not_call_model_or_duplicate_candidate(self):
        self.publish_fixture()
        request = self.enqueue(refresh_source=True)
        def should_not_run(*_):
            self.fail("same frozen prefix called the model")
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, should_not_run), 1)
        state = json.loads(request.read_text())
        self.assertEqual((state["status"], state["validation"], state["published"]), ("completed", "unchanged", True))
        self.assertEqual(state["source_sha256"], self.sha)
        self.assertFalse((self.data / "v2" / "review" / f"{SESSION}.json").exists())
        self.assertFalse((self.data / "v2" / "review-sources" / f"{SESSION}.json").exists())

    def test_published_same_prefix_is_idempotent_without_refresh_flag(self):
        self.publish_fixture()
        request = self.enqueue(source_sha256=self.sha)
        def should_not_run(*_):
            self.fail("same published source called the model")
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, should_not_run), 1)
        state = json.loads(request.read_text())
        self.assertEqual((state["status"], state["validation"], state["published"]), ("completed", "unchanged", True))
        self.assertFalse((self.data / "v2" / "review" / f"{SESSION}.json").exists())

    def test_refresh_grown_prefix_stages_review_without_changing_published_source(self):
        old_descriptor = self.publish_fixture()
        with self.source.open("a") as output:
            output.write(json.dumps({"type": "event_msg", "payload": {"type": "task_complete"}}) + "\n")
        new_sha = hashlib.sha256(self.source.read_bytes()).hexdigest()
        request = self.enqueue(refresh_source=True)
        prompts = []
        def revised_executor(prompt, output_path):
            prompts.append(prompt)
            output_path.write_text(json.dumps(report(new_sha)))
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, revised_executor), 1)
        state = json.loads(request.read_text())
        self.assertEqual((state["status"], state["published"], state["source_sha256"]), ("completed", False, new_sha))
        self.assertEqual(len(prompts), 1)
        self.assertIn("уже применённую правку не предлагай повторно", prompts[0])
        self.assertEqual(json.loads((self.data / "v2" / "sources" / f"{SESSION}.json").read_text()), old_descriptor)
        self.assertEqual(json.loads((self.data / "v2" / "deep" / f"{SESSION}.json").read_text())["source_sha256"], self.sha)
        self.assertEqual(json.loads((self.data / "v2" / "review-sources" / f"{SESSION}.json").read_text())["source_sha256"], new_sha)
        self.assertEqual(json.loads((self.data / "v2" / "review" / f"{SESSION}.json").read_text())["source_sha256"], new_sha)
        self.assertEqual(json.loads((self.data / "v2" / "review-telemetry" / SESSION / "telemetry.json").read_text())["source"]["sha256"], new_sha)
        # A second request for the same unpublished version reuses the review candidate.
        state.update(status="pending", requested_at="2026-01-02T00:00:00Z")
        v2_worker.atomic_json(request, state)
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, revised_executor), 1)
        self.assertEqual(len(prompts), 1)
        (self.data / "v2" / "review-telemetry" / SESSION / "telemetry.json").unlink()
        v2_worker.atomic_json(request, {**state, "status": "pending"})
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, revised_executor), 1)
        self.assertEqual(len(prompts), 2)

    def test_applied_change_is_in_prompt_and_exact_duplicate_is_rejected(self):
        self.publish_fixture()
        with self.source.open("a") as output:
            output.write(json.dumps({"type": "event_msg", "payload": {"type": "task_complete"}}) + "\n")
        new_sha = hashlib.sha256(self.source.read_bytes()).hexdigest()
        proposal = proposal_example()
        self.assertNotEqual(proposal["proposal_id"], proposal["group_key"])  # registry uses group_key as published ID
        proposal["sources"][0].update(session_id=SESSION, task_id="t1", source_sha256=new_sha)
        event = {
            "schema_version": 1, "event_id": "event-1", "recorded_at": "2026-01-01T00:00:00Z",
            "proposal_id": proposal["group_key"], "proposal_fingerprint": v2_lifecycle.proposal_fingerprint(proposal),
            "event": "application", "actor": "owner", "authority_ref": "approval-1",
            "proposal_sources": proposal["sources"],
            "detail": {"version": "v2", "target_version_verified": "v1", "evidence": ["local test"], "permission_ref": "approval-1", "rule_review": "checked"},
            "previous_hash": v2_lifecycle.ZERO_HASH,
        }
        event["record_hash"] = hashlib.sha256(v2_lifecycle.canonical(event)).hexdigest()
        journal = self.data / "v2" / "lifecycle" / "events.jsonl"
        v2_worker.private_dir(journal.parent)
        journal.write_text(json.dumps(event) + "\n")
        journal.chmod(0o600)
        request = self.enqueue(refresh_source=True)
        prompts = []
        def duplicate_executor(prompt, output_path):
            prompts.append(prompt)
            result = report(new_sha)
            result["proposal_candidates"] = [proposal]
            output_path.write_text(json.dumps(result))
        self.assertEqual(v2_worker.run_once(self.data, self.sessions, duplicate_executor), 1)
        state = json.loads(request.read_text())
        self.assertEqual((state["status"], state["error_code"]), ("failed", "invalid_analysis"))
        self.assertEqual(len(prompts), 2)
        self.assertIn(str(journal), prompts[0])
        self.assertIn("already recorded as applied", prompts[1])
        self.assertFalse((self.data / "v2" / "review" / f"{SESSION}.json").exists())


if __name__ == "__main__":
    unittest.main()
