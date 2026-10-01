"""Synthetic structural checks; private session/eval material stays outside Git."""

import copy
import unittest

from analytics.v2_contract import CHECK_IDS, REVIEW_CHECKS, proposal_group_key, validate_deep, validate_registry, validate_semantic_review


SID = "session-demo"
SHA = "a" * 64
RECORDS = 12


def report_example():
    return {
        "kind": "deep", "schema_version": 2, "session_id": SID, "source_sha256": SHA,
        "tasks": [{
            "task_id": "task-1", "goal": "Create an artifact", "goal_evidence": ["L1"],
            "scope": "local example", "start_line": 1, "end_line": 12,
            "boundary_evidence": ["L1", "L12"], "success_criteria": ["Artifact exists"],
            "outcome": "unknown", "outcome_basis": ["L12"], "claims": [],
        }],
        "checks": [{"id": check_id, "status": "not_checked", "summary": "Pending review", "evidence": [], "missing_data": [], "task_ids": ["task-1"]} for check_id in CHECK_IDS],
        "observations": [], "proposal_candidates": [], "unknowns": ["Human acceptance is unknown"],
    }


def proposal_example():
    pattern, scope, intent = "PREFERENCE", "example project", "preserve concise output"
    return {
        "proposal_id": "P-example", "group_key": proposal_group_key(pattern, scope, intent),
        "pattern_id": pattern, "change_intent": intent, "action": "Review the preference",
        "observed": "A preference was stated", "cause": "", "alternative_causes": [],
        "existing_rule": {"status": "not_checked", "description": "", "locator": "", "version": ""},
        "change_type": "personalization", "selection_reason": "Direct preference", "scope": scope,
        "target": {"kind": "personalization", "locator": "", "version": ""},
        "change": "", "preconditions": [], "exceptions": [], "verification": "", "rollback": "",
        "expected_effect": "", "priority": "medium",
        "readiness": {"status": "hypothesis", "reason": "Locate the current preference first"},
        "decision": {"status": "not_requested", "at": ""},
        "execution": {"status": "not_applied", "at": "", "version": "", "evidence": []},
        "effect": {"status": "not_measured", "method": "", "metric": "", "n_before": 0, "n_after": 0, "note": ""},
        "sources": [{"session_id": SID, "task_id": "task-1", "source_sha256": SHA, "evidence": ["L2"]}],
        "analysis_versions": ["2.0"],
    }


class V2ContractTest(unittest.TestCase):
    def test_continued_task_checks_both_frozen_sessions(self):
        earlier = report_example()
        earlier["session_id"] = "earlier-session"
        earlier["tasks"][0]["end_line"] = 6
        earlier["tasks"][0]["boundary_evidence"] = ["L1", "L6"]
        earlier["tasks"][0]["outcome_basis"] = ["L6"]
        current = report_example()
        current["session_id"] = "current-session"
        current["source_sha256"] = "b" * 64
        current["tasks"][0]["start_line"] = 7
        current["tasks"][0]["goal_evidence"] = ["L7"]
        current["tasks"][0]["boundary_evidence"] = ["L7", "L12"]
        current["tasks"][0]["continued_from"] = {
            "session_id": "earlier-session", "task_id": "task-1", "source_sha256": SHA,
            "current_evidence": ["L7"], "previous_evidence": ["L2"],
        }
        registry = {"kind": "proposal_registry", "schema_version": 2, "proposals": []}
        reports = {"earlier-session": (earlier, RECORDS), "current-session": (current, RECORDS)}
        validate_registry(registry, reports)

        current["tasks"][0]["continued_from"]["current_evidence"] = ["L2"]
        with self.assertRaisesRegex(ValueError, "outside current task boundary"):
            validate_registry(registry, reports)
        current["tasks"][0]["continued_from"]["current_evidence"] = ["L7"]
        current["tasks"][0]["continued_from"]["previous_evidence"] = ["L9"]
        with self.assertRaisesRegex(ValueError, "previous evidence outside cited task boundary"):
            validate_registry(registry, reports)
        current["tasks"][0]["continued_from"]["previous_evidence"] = ["L2"]
        current["tasks"][0]["continued_from"]["source_sha256"] = "c" * 64
        with self.assertRaisesRegex(ValueError, "previous task or frozen version mismatch"):
            validate_registry(registry, reports)

    def test_semantic_review_is_bound_to_exact_candidate(self):
        review = {"session_id": SID, "candidate_sha256": SHA, "verdict": "approved", "reviewer": "independent-agent",
                  "reviewed_at": "2026-09-30T00:00:00Z", "note": "Read source and checked the conclusions",
                  "checks": sorted(REVIEW_CHECKS)}
        validate_semantic_review(review, session_id=SID, candidate_sha256=SHA)
        with self.assertRaisesRegex(ValueError, "version mismatch"):
            validate_semantic_review(review, session_id=SID, candidate_sha256="b" * 64)
        review["checks"].pop()
        with self.assertRaisesRegex(ValueError, "all independent semantic checks"):
            validate_semantic_review(review, session_id=SID, candidate_sha256=SHA)

    def test_multitask_report_with_early_completion_claim(self):
        report = report_example()
        report["tasks"][0]["end_line"] = 6
        report["tasks"][0]["boundary_evidence"] = ["L1", "L6"]
        report["tasks"][0]["claims"] = [{
            "line": 4, "claim": "The work is done", "verification_at_claim": "unverified",
            "evidence": ["L4"], "subsequent_resolution": "Corrected later",
        }]
        report["tasks"].append({
            "task_id": "task-2", "goal": "Revise the artifact", "goal_evidence": ["L7"],
            "scope": "local example", "start_line": 7, "end_line": 12,
            "boundary_evidence": ["L7", "L12"], "success_criteria": [],
            "outcome": "unknown", "outcome_basis": ["L12"], "claims": [],
        })
        report["checks"][2].update(status="suspected", summary="Earlier completion lacked a check", evidence=["L4"], task_ids=["task-1"])
        validate_deep(report, session_id=SID, source_sha256=SHA, source_records=RECORDS)

    def test_d05_requires_claim_and_frozen_line(self):
        report = report_example()
        report["checks"][2].update(status="suspected", summary="Claim", evidence=["L4"])
        with self.assertRaisesRegex(ValueError, "D05 signal"):
            validate_deep(report, session_id=SID, source_sha256=SHA, source_records=RECORDS)
        report["checks"][2]["evidence"] = ["L13"]
        with self.assertRaisesRegex(ValueError, "outside frozen source"):
            validate_deep(report, session_id=SID, source_sha256=SHA, source_records=RECORDS)

    def test_clear_requires_reviewed_evidence_and_all_checks(self):
        report = report_example()
        report["checks"][0]["status"] = "checked_clear"
        with self.assertRaisesRegex(ValueError, "expected string array"):
            validate_deep(report, session_id=SID, source_sha256=SHA, source_records=RECORDS)
        report = report_example()
        report["checks"].pop()
        with self.assertRaisesRegex(ValueError, "all 13 checks"):
            validate_deep(report, session_id=SID, source_sha256=SHA, source_records=RECORDS)

    def test_hypothesis_is_valid_but_prepared_needs_exact_change(self):
        report = report_example()
        report["proposal_candidates"] = [proposal_example()]
        validate_deep(report, session_id=SID, source_sha256=SHA, source_records=RECORDS)
        report["proposal_candidates"][0]["readiness"] = {"status": "prepared_verified", "reason": "Prepared"}
        with self.assertRaisesRegex(ValueError, "expected nonempty string"):
            validate_deep(report, session_id=SID, source_sha256=SHA, source_records=RECORDS)

    def test_registry_deduplicates_group_and_rejects_unapplied_effect(self):
        report = report_example()
        candidate = proposal_example()
        report["proposal_candidates"] = [candidate]
        registry = {"kind": "proposal_registry", "schema_version": 2, "proposals": [copy.deepcopy(candidate)]}
        validate_registry(registry, {SID: (report, RECORDS)})
        registry["proposals"].append(copy.deepcopy(candidate))
        with self.assertRaisesRegex(ValueError, "duplicate proposal"):
            validate_registry(registry, {SID: (report, RECORDS)})
        registry["proposals"].pop()
        registry["proposals"][0]["effect"]["status"] = "helped"
        with self.assertRaisesRegex(ValueError, "before application"):
            validate_registry(registry, {SID: (report, RECORDS)})

    def test_source_task_and_version_are_checked(self):
        report = report_example()
        candidate = proposal_example()
        candidate["sources"][0]["source_sha256"] = "b" * 64
        report["proposal_candidates"] = [candidate]
        with self.assertRaisesRegex(ValueError, "frozen version mismatch"):
            validate_deep(report, session_id=SID, source_sha256=SHA, source_records=RECORDS)

    def test_proposal_evidence_must_belong_to_its_cited_task(self):
        report = report_example()
        report["tasks"][0]["end_line"] = 6
        candidate = proposal_example()
        candidate["sources"][0]["evidence"] = ["L8"]
        report["proposal_candidates"] = [candidate]
        with self.assertRaisesRegex(ValueError, "outside cited task boundary"):
            validate_deep(report, session_id=SID, source_sha256=SHA, source_records=RECORDS)
        report["proposal_candidates"] = []
        registry = {"kind": "proposal_registry", "schema_version": 2, "proposals": [candidate]}
        with self.assertRaisesRegex(ValueError, "outside cited task boundary"):
            validate_registry(registry, {SID: (report, RECORDS)})

    def test_group_key_ignores_case_and_spacing(self):
        self.assertEqual(
            proposal_group_key("D05", " Project  A ", " Check result "),
            proposal_group_key("d05", "project a", "check result"),
        )

    def test_d22_peer_source_is_checked_against_peer_task_and_version(self):
        local = report_example()
        peer = report_example()
        peer["session_id"] = "session-peer"
        peer["source_sha256"] = "b" * 64
        peer["tasks"][0]["outcome"] = "verified"
        source = {"session_id": "session-peer", "task_id": "task-1", "source_sha256": "b" * 64, "evidence": ["L2"]}
        local["checks"][9]["peer_sources"] = [source]
        registry = {"kind": "proposal_registry", "schema_version": 2, "proposals": []}
        reports = {SID: (local, RECORDS), "session-peer": (peer, RECORDS)}
        validate_registry(registry, reports)
        source["source_sha256"] = "c" * 64
        with self.assertRaisesRegex(ValueError, "peer task or frozen version mismatch"):
            validate_registry(registry, reports)
        source["source_sha256"] = "b" * 64
        source["evidence"] = ["L13"]
        with self.assertRaisesRegex(ValueError, "outside frozen source"):
            validate_registry(registry, reports)
        source["evidence"] = ["L2"]
        peer["tasks"][0]["outcome"] = "unknown"
        with self.assertRaisesRegex(ValueError, "verified outcome"):
            validate_registry(registry, reports)


if __name__ == "__main__":
    unittest.main()
