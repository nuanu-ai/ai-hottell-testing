import copy
import json
import os
import tempfile
import unittest
from pathlib import Path

from v2_lifecycle import journal_path, project_proposal, read_events, record_event
from test_v2_registry import candidate
from v2_registry import merge_candidates


class LifecycleJournalTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.proposal = candidate("s1", "t1")
        self.proposal["proposal_id"] = self.proposal["group_key"]
        self.proposal["target"] = {"kind": "file", "locator": "rules.md", "version": "sha-old"}
        self.proposal["existing_rule"]["status"] = "not_found"
        self.proposal["change"] = "Add one scoped rule"
        self.proposal["verification"] = "Replay comparable tasks"
        self.proposal["rollback"] = "Restore sha-old"
        self.proposal["expected_effect"] = "Fewer repeated instructions"
        self.proposal["preconditions"] = ["Owner confirms scope"]
        self.proposal["readiness"] = {"status": "prepared_verified", "reason": "Target and diff checked"}
        registry = self.root / "v2" / "proposals.json"
        registry.parent.mkdir()
        registry.write_text(json.dumps({"kind": "proposal_registry", "schema_version": 2, "proposals": [self.proposal]}))
        os.chmod(registry, 0o600)

    def request(self, event, detail):
        return {"proposal_id": self.proposal["proposal_id"], "event": event,
                "actor": "owner-or-reviewer", "authority_ref": "private approval reference",
                "detail": detail}

    def accept(self):
        record_event(self.root, self.request("decision", {"status": "accepted"}))

    def apply(self):
        record_event(self.root, self.request("application", {
            "version": "sha-new", "target_version_verified": "sha-old",
            "evidence": ["local commit sha-new"], "permission_ref": "owner approval",
            "rule_review": "No equivalent rule at current target",
        }))

    def test_full_cycle_is_private_and_append_only(self):
        self.accept()
        self.apply()
        record_event(self.root, self.request("effect", {
            "status": "helped", "method": "before_after", "metric": "repeated instructions",
            "n_before": 3, "n_after": 4, "note": "Comparable task sample",
            "evidence": ["private comparison report"],
        }))
        path = journal_path(self.root)
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(path.parent.stat().st_mode & 0o777, 0o700)
        events = read_events(self.root)
        self.assertEqual([event["event"] for event in events], ["decision", "application", "effect"])
        state = project_proposal(self.proposal, events)
        self.assertEqual(state["decision"]["status"], "accepted")
        self.assertEqual(state["execution"]["version"], "sha-new")
        self.assertEqual(state["effect"]["status"], "helped")
        self.assertEqual(events[1]["proposal_sources"], self.proposal["sources"])
        merged = merge_candidates({"s1": ({"proposal_candidates": [self.proposal]}, 1)}, lifecycle_events=events)
        self.assertEqual(merged["proposals"][0]["effect"]["status"], "helped")

    def test_effect_before_application_and_stale_target_are_rejected(self):
        effect = self.request("effect", {"status": "insufficient_data", "method": "", "metric": "",
                                         "n_before": 0, "n_after": 0, "note": "Need more tasks", "evidence": ["review note"]})
        with self.assertRaisesRegex(ValueError, "effect cannot precede"):
            record_event(self.root, effect)
        self.accept()
        bad_apply = self.request("application", {"version": "sha-new", "target_version_verified": "wrong",
                                                  "evidence": ["local commit"], "permission_ref": "approval", "rule_review": "checked"})
        with self.assertRaisesRegex(ValueError, "target version changed"):
            record_event(self.root, bad_apply)
        self.assertEqual(len(read_events(self.root)), 1)

    def test_changed_target_needs_new_decision_and_old_application_stays_in_history(self):
        self.accept()
        self.apply()
        changed = copy.deepcopy(self.proposal)
        changed["target"]["version"] = "sha-new"
        changed["readiness"] = {"status": "needs_specification", "reason": "Recheck target"}
        state = project_proposal(changed, read_events(self.root))
        self.assertEqual(state["decision"]["status"], "not_requested")
        self.assertEqual(state["execution"]["status"], "not_applied")
        self.assertEqual(state["readiness"]["status"], "needs_specification")
        self.assertIn("private lifecycle journal", state["history_review"])
        self.assertEqual(len(read_events(self.root)), 2)
        previous = copy.deepcopy(self.proposal)
        previous["decision"] = {"status": "accepted", "at": read_events(self.root)[0]["recorded_at"]}
        previous["execution"] = {"status": "applied", "at": read_events(self.root)[1]["recorded_at"], "version": "sha-new", "evidence": ["local commit sha-new"]}
        merged = merge_candidates({"s1": ({"proposal_candidates": [changed]}, 1)},
                                  {"proposals": [previous]}, read_events(self.root))["proposals"][0]
        self.assertEqual(merged["execution"]["status"], "not_applied")
        self.assertEqual(merged["decision"]["status"], "not_requested")

    def test_hash_chain_detects_modified_history(self):
        self.accept()
        path = journal_path(self.root)
        content = path.read_text().replace("private approval reference", "different approval reference")
        path.write_text(content)
        with self.assertRaisesRegex(ValueError, "hash mismatch"):
            read_events(self.root)

    def test_later_analysis_conflict_keeps_recorded_application_visible(self):
        self.accept()
        self.apply()
        reanalyzed = copy.deepcopy(self.proposal)
        reanalyzed["readiness"] = {"status": "needs_specification", "reason": "Conflicting new evidence"}
        state = project_proposal(reanalyzed, read_events(self.root))
        self.assertEqual(state["execution"]["status"], "applied")
        self.assertIn("New analysis questions readiness", state["history_review"])


if __name__ == "__main__":
    unittest.main()
