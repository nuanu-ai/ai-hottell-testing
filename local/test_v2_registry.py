import copy
import tempfile
import unittest
from pathlib import Path

from v2_registry import load_manifest, merge_candidates, select_source
from analytics.v2_contract import proposal_group_key


def candidate(sid, task_id, cause="likely cause"):
    pattern, scope, intent = "D10", "project-a", "make the known rule visible"
    return {
        "proposal_id": sid, "group_key": proposal_group_key(pattern, scope, intent),
        "pattern_id": pattern, "change_intent": intent, "action": "Review one rule",
        "observed": "The same instruction was repeated", "cause": cause,
        "alternative_causes": [], "existing_rule": {"status": "not_checked", "description": "", "locator": "", "version": ""},
        "change_type": "project_rule", "selection_reason": "project scoped", "scope": scope,
        "target": {"kind": "file", "locator": "", "version": ""},
        "change": "", "preconditions": [], "exceptions": [], "verification": "", "rollback": "", "expected_effect": "",
        "priority": "medium", "readiness": {"status": "hypothesis", "reason": "Inspect current rule"},
        "decision": {"status": "not_requested", "at": ""},
        "execution": {"status": "not_applied", "at": "", "version": "", "evidence": []},
        "effect": {"status": "not_measured", "method": "", "metric": "", "n_before": 0, "n_after": 0, "note": ""},
        "sources": [{"session_id": sid, "task_id": task_id, "source_sha256": "a" * 64, "evidence": ["L1"]}],
        "analysis_versions": ["v2"],
    }


class RegistryMergeTest(unittest.TestCase):
    def test_new_session_store_does_not_require_historical_manifest(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.assertEqual(load_manifest(root, published=True), {"sessions": []})
            with self.assertRaises(FileNotFoundError):
                load_manifest(root, published=False)

    def test_deduplicates_same_intent_and_keeps_both_sources(self):
        a, b = candidate("s1", "t1"), candidate("s2", "t1")
        registry = merge_candidates({"s1": ({"proposal_candidates": [a]}, 1),
                                     "s2": ({"proposal_candidates": [b]}, 1)})
        self.assertEqual(len(registry["proposals"]), 1)
        self.assertEqual({s["session_id"] for s in registry["proposals"][0]["sources"]}, {"s1", "s2"})
        self.assertIn("в 2 сессиях", registry["proposals"][0]["observed"])

    def test_conflicting_cause_is_not_silently_merged(self):
        a, b = candidate("s1", "t1"), candidate("s2", "t1", "different cause")
        registry = merge_candidates({"s1": ({"proposal_candidates": [a]}, 1),
                                     "s2": ({"proposal_candidates": [b]}, 1)})
        self.assertEqual(registry["proposals"][0]["readiness"]["status"], "needs_specification")

    def test_uncertain_diagnosis_keeps_rule_gaps_visible_without_false_conflict(self):
        a, b = candidate("s1", "t1", "Unknown collector stage"), candidate("s2", "t1", "Unknown dashboard stage")
        a["change_type"] = b["change_type"] = "diagnosis"
        b["existing_rule"] = {"status": "found", "description": "historical rule", "locator": "snapshot", "version": ""}
        registry = merge_candidates({"s1": ({"proposal_candidates": [a]}, 1),
                                     "s2": ({"proposal_candidates": [b]}, 1)})
        item = registry["proposals"][0]
        self.assertEqual(item["readiness"]["status"], "hypothesis")
        self.assertIn("Cause remains uncertain", item["history_review"])
        self.assertIn("Historical rule availability", item["history_review"])
        self.assertNotIn("Conflicting details", item["history_review"])

    def test_new_analysis_cannot_claim_human_decision(self):
        a = candidate("s1", "t1")
        a["decision"]["status"] = "accepted"
        with self.assertRaises(ValueError):
            merge_candidates({"s1": ({"proposal_candidates": [a]}, 1)})

    def test_prior_state_is_not_copied_without_journal(self):
        a = candidate("s1", "t1")
        old = copy.deepcopy(a)
        old["decision"] = {"status": "accepted", "at": "2026-09-30T00:00:00Z"}
        old["proposal_id"] = old["group_key"]
        out = merge_candidates({"s1": ({"proposal_candidates": [a]}, 1)},
                               {"proposals": [old]})["proposals"][0]
        self.assertEqual(out["decision"]["status"], "not_requested")
        a["target"]["version"] = "new version"
        out = merge_candidates({"s1": ({"proposal_candidates": [a]}, 1)},
                               {"proposals": [old]})["proposals"][0]
        self.assertEqual(out["decision"]["status"], "not_requested")
        self.assertEqual(out["readiness"]["status"], "needs_specification")

    def test_published_deep_selects_matching_v2_frozen_source(self):
        old = {"source_sha256": "a" * 64, "source_records": 10}
        newer = {"source_sha256": "b" * 64, "source_records": 20}
        self.assertIs(select_source(old, newer, {"source_sha256": "a" * 64}), old)
        self.assertIs(select_source(old, newer, {"source_sha256": "b" * 64}), newer)


if __name__ == "__main__":
    unittest.main()
