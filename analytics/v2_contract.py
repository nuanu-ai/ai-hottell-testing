"""Structural contract for private AI Hottell 2.0 Deep reports and proposals.

This module validates provenance and state coherence. It cannot establish whether
an agent's interpretation of a session is correct; that needs a separate review.
Legacy reports have no schema_version and are intentionally outside this contract.
"""

from __future__ import annotations

import hashlib
import json
import re
import unicodedata
from collections.abc import Mapping


CHECK_IDS = ("D01", "D03", "D05", "D07", "D10", "D12", "D13", "D16", "D19", "D22", "D24", "D25", "D26")
REVIEW_CHECKS = {"task_boundaries", "completion_claims", "all_13_checks", "proposal_choice", "sensitive_data"}
CHECK_STATUSES = {"suspected", "checked_clear", "insufficient_data", "not_applicable", "not_checked"}
OUTCOMES = {"verified", "partial", "failed", "unknown"}
CHANGE_TYPES = {"personalization", "project_rule", "skill", "hook", "script", "workflow", "automation", "settings", "product", "diagnosis"}
READINESS = {"hypothesis", "needs_specification", "prepared_verified"}
DECISIONS = {"not_requested", "accepted", "rejected", "revision_requested"}
EXECUTIONS = {"not_applied", "applied"}
EFFECTS = {"not_measured", "helped", "no_effect", "worse", "insufficient_data"}
LINE_REF = re.compile(r"^L([1-9][0-9]*)$")
SHA256 = re.compile(r"^[0-9a-f]{64}$")


def _fail(path: str, reason: str) -> None:
    raise ValueError(f"{path}: {reason}")


def _object(value: object, path: str, required: set[str], optional: set[str] = frozenset()) -> Mapping:
    if not isinstance(value, dict):
        _fail(path, "expected object")
    missing = required - value.keys()
    extra = value.keys() - required - optional
    if missing or extra:
        _fail(path, f"missing {sorted(missing)}, unexpected {sorted(extra)}")
    return value


def _string(value: object, path: str, *, nonempty: bool = False) -> str:
    if not isinstance(value, str) or (nonempty and not value.strip()):
        _fail(path, "expected nonempty string" if nonempty else "expected string")
    return value


def _strings(value: object, path: str, *, nonempty: bool = False) -> list[str]:
    if not isinstance(value, list) or (nonempty and not value):
        _fail(path, "expected string array")
    for index, item in enumerate(value):
        _string(item, f"{path}[{index}]", nonempty=True)
    return value


def _choice(value: object, path: str, allowed: set[str] | tuple[str, ...]) -> None:
    if not isinstance(value, str) or value not in allowed:
        _fail(path, f"must be one of {sorted(allowed)}")


def proposal_group_key(pattern_id: str, scope: str, change_intent: str) -> str:
    """Stable key for a reviewed semantic change intent, independent of session ID."""
    def normalize(value: str) -> str:
        return " ".join(unicodedata.normalize("NFKC", value).casefold().split())

    components = [normalize(value) for value in (pattern_id, scope, change_intent)]
    if not all(components):
        raise ValueError("group key components must be nonempty")
    digest = hashlib.sha256(json.dumps(components, ensure_ascii=False, separators=(",", ":")).encode()).hexdigest()
    return f"p2:{digest[:20]}"


def _positive_line(value: object, path: str, source_records: int) -> None:
    if type(value) is not int or not 1 <= value <= source_records:
        _fail(path, f"line outside frozen source 1..{source_records}")


def _evidence(value: object, path: str, source_records: int, *, nonempty: bool = True) -> None:
    for index, ref in enumerate(_strings(value, path, nonempty=nonempty)):
        match = LINE_REF.fullmatch(ref)
        if not match:
            _fail(f"{path}[{index}]", "expected local source reference L<number>")
        _positive_line(int(match.group(1)), f"{path}[{index}]", source_records)


def _validate_task(task: object, path: str, source_records: int) -> str:
    task = _object(task, path, {"task_id", "goal", "goal_evidence", "scope", "start_line", "end_line", "boundary_evidence", "success_criteria", "outcome", "outcome_basis", "claims"}, {"continued_from"})
    task_id = _string(task["task_id"], f"{path}.task_id", nonempty=True)
    _string(task["goal"], f"{path}.goal", nonempty=True)
    _string(task["scope"], f"{path}.scope", nonempty=True)
    start, end = task["start_line"], task["end_line"]
    _positive_line(start, f"{path}.start_line", source_records)
    _positive_line(end, f"{path}.end_line", source_records)
    if start > end:
        _fail(path, "task starts after it ends")
    _evidence(task["goal_evidence"], f"{path}.goal_evidence", source_records)
    _evidence(task["boundary_evidence"], f"{path}.boundary_evidence", source_records)
    _strings(task["success_criteria"], f"{path}.success_criteria")
    _choice(task["outcome"], f"{path}.outcome", OUTCOMES)
    _evidence(task["outcome_basis"], f"{path}.outcome_basis", source_records)
    if not isinstance(task["claims"], list):
        _fail(f"{path}.claims", "expected array")
    for index, claim in enumerate(task["claims"]):
        cp = f"{path}.claims[{index}]"
        claim = _object(claim, cp, {"line", "claim", "verification_at_claim", "evidence", "subsequent_resolution"})
        _positive_line(claim["line"], f"{cp}.line", source_records)
        if not start <= claim["line"] <= end:
            _fail(f"{cp}.line", "completion claim outside task boundary")
        _string(claim["claim"], f"{cp}.claim", nonempty=True)
        _choice(claim["verification_at_claim"], f"{cp}.verification_at_claim", {"verified", "unverified", "contradicted", "unknown"})
        _evidence(claim["evidence"], f"{cp}.evidence", source_records)
        _string(claim["subsequent_resolution"], f"{cp}.subsequent_resolution")
    if "continued_from" in task:
        cp = f"{path}.continued_from"
        continuation = _object(task["continued_from"], cp,
                               {"session_id", "task_id", "source_sha256", "current_evidence", "previous_evidence"})
        _string(continuation["session_id"], f"{cp}.session_id", nonempty=True)
        _string(continuation["task_id"], f"{cp}.task_id", nonempty=True)
        if not SHA256.fullmatch(_string(continuation["source_sha256"], f"{cp}.source_sha256")):
            _fail(f"{cp}.source_sha256", "invalid frozen source version")
        _evidence(continuation["current_evidence"], f"{cp}.current_evidence", source_records)
        if any(not start <= int(ref[1:]) <= end for ref in continuation["current_evidence"]):
            _fail(f"{cp}.current_evidence", "outside current task boundary")
        for index, ref in enumerate(_strings(continuation["previous_evidence"], f"{cp}.previous_evidence", nonempty=True)):
            if not LINE_REF.fullmatch(ref):
                _fail(f"{cp}.previous_evidence[{index}]", "expected frozen source reference L<number>")
    return task_id


def _task_ids(value: object, path: str, task_ids: set[str]) -> None:
    ids = _strings(value, path)
    if len(ids) != len(set(ids)) or any(task_id not in task_ids for task_id in ids):
        _fail(path, "duplicate or unknown task id")


def _validate_check(check: object, path: str, source_records: int, task_ids: set[str]) -> None:
    check = _object(check, path, {"id", "status", "summary", "evidence", "missing_data", "task_ids"}, {"peer_sources"})
    _choice(check["id"], f"{path}.id", CHECK_IDS)
    status = check["status"]
    _choice(status, f"{path}.status", CHECK_STATUSES)
    _string(check["summary"], f"{path}.summary", nonempty=True)
    _evidence(check["evidence"], f"{path}.evidence", source_records, nonempty=status in {"suspected", "checked_clear"})
    _strings(check["missing_data"], f"{path}.missing_data", nonempty=status == "insufficient_data")
    _task_ids(check["task_ids"], f"{path}.task_ids", task_ids)
    if status in {"suspected", "checked_clear"} and not check["task_ids"]:
        _fail(path, "assessed check must identify tasks")
    if "peer_sources" in check:
        if not isinstance(check["peer_sources"], list):
            _fail(f"{path}.peer_sources", "expected array")
        for index, source in enumerate(check["peer_sources"]):
            sp = f"{path}.peer_sources[{index}]"
            source = _object(source, sp, {"session_id", "task_id", "source_sha256", "evidence"})
            for key in ("session_id", "task_id"):
                _string(source[key], f"{sp}.{key}", nonempty=True)
            if not isinstance(source["source_sha256"], str) or not SHA256.fullmatch(source["source_sha256"]):
                _fail(f"{sp}.source_sha256", "expected SHA-256")
            for ref in _strings(source["evidence"], f"{sp}.evidence", nonempty=True):
                if not LINE_REF.fullmatch(ref):
                    _fail(f"{sp}.evidence", "expected L<number> reference")


def _validate_observation(observation: object, path: str, source_records: int, task_ids: set[str]) -> None:
    observation = _object(observation, path, {"pattern", "finding", "status", "evidence", "task_ids"})
    _string(observation["pattern"], f"{path}.pattern", nonempty=True)
    _string(observation["finding"], f"{path}.finding", nonempty=True)
    _choice(observation["status"], f"{path}.status", {"suspected", "confirmed", "dismissed"})
    _evidence(observation["evidence"], f"{path}.evidence", source_records)
    _task_ids(observation["task_ids"], f"{path}.task_ids", task_ids)
    if not observation["task_ids"]:
        _fail(path, "observation must identify a task")


def _validate_proposal(proposal: object, path: str, report_index: Mapping[str, tuple[Mapping[str, tuple[int, int]], int, str]], *, candidate_session: str | None = None) -> None:
    required = {"proposal_id", "group_key", "pattern_id", "change_intent", "action", "observed", "cause", "alternative_causes", "existing_rule", "change_type", "selection_reason", "scope", "target", "change", "preconditions", "exceptions", "verification", "rollback", "expected_effect", "priority", "readiness", "decision", "execution", "effect", "sources", "analysis_versions"}
    proposal = _object(proposal, path, required, {"automation_basis", "history_review"})
    for key in ("proposal_id", "group_key", "pattern_id", "change_intent", "action", "observed", "selection_reason", "scope"):
        _string(proposal[key], f"{path}.{key}", nonempty=True)
    expected_group = proposal_group_key(proposal["pattern_id"], proposal["scope"], proposal["change_intent"])
    if proposal["group_key"] != expected_group:
        _fail(f"{path}.group_key", "does not match pattern, scope and change intent")
    for key in ("cause", "change", "verification", "rollback", "expected_effect"):
        _string(proposal[key], f"{path}.{key}")
    for key in ("alternative_causes", "preconditions", "exceptions", "analysis_versions"):
        _strings(proposal[key], f"{path}.{key}")
    _choice(proposal["change_type"], f"{path}.change_type", CHANGE_TYPES)
    _choice(proposal["priority"], f"{path}.priority", {"low", "medium", "high"})
    rule = _object(proposal["existing_rule"], f"{path}.existing_rule", {"status", "description", "locator", "version"})
    _choice(rule["status"], f"{path}.existing_rule.status", {"found", "not_found", "not_checked"})
    for key in ("description", "locator", "version"):
        _string(rule[key], f"{path}.existing_rule.{key}")
    target = _object(proposal["target"], f"{path}.target", {"kind", "locator", "version"})
    for key in ("kind", "locator", "version"):
        _string(target[key], f"{path}.target.{key}")
    readiness = _object(proposal["readiness"], f"{path}.readiness", {"status", "reason"})
    _choice(readiness["status"], f"{path}.readiness.status", READINESS)
    _string(readiness["reason"], f"{path}.readiness.reason", nonempty=True)
    decision = _object(proposal["decision"], f"{path}.decision", {"status", "at"})
    _choice(decision["status"], f"{path}.decision.status", DECISIONS)
    _string(decision["at"], f"{path}.decision.at")
    execution = _object(proposal["execution"], f"{path}.execution", {"status", "at", "version", "evidence"})
    _choice(execution["status"], f"{path}.execution.status", EXECUTIONS)
    _string(execution["at"], f"{path}.execution.at")
    _string(execution["version"], f"{path}.execution.version")
    _strings(execution["evidence"], f"{path}.execution.evidence")
    effect = _object(proposal["effect"], f"{path}.effect", {"status", "method", "metric", "n_before", "n_after", "note"})
    _choice(effect["status"], f"{path}.effect.status", EFFECTS)
    for key in ("method", "metric", "note"):
        _string(effect[key], f"{path}.effect.{key}")
    for key in ("n_before", "n_after"):
        if type(effect[key]) is not int or effect[key] < 0:
            _fail(f"{path}.effect.{key}", "expected nonnegative integer")
    if readiness["status"] == "prepared_verified":
        for key, value in (("cause", proposal["cause"]), ("change", proposal["change"]), ("verification", proposal["verification"]), ("rollback", proposal["rollback"]), ("expected_effect", proposal["expected_effect"]), ("target.kind", target["kind"]), ("target.locator", target["locator"]), ("target.version", target["version"])):
            _string(value, f"{path}.{key}", nonempty=True)
        if rule["status"] == "not_checked" or not proposal["preconditions"]:
            _fail(path, "prepared proposal needs existing-rule review and preconditions")
    if execution["status"] == "applied":
        if readiness["status"] != "prepared_verified" or decision["status"] != "accepted" or not execution["at"] or not execution["version"] or not execution["evidence"]:
            _fail(path, "applied change needs prepared change, accepted decision, time, version, evidence")
    elif effect["status"] != "not_measured":
        _fail(path, "effect cannot be measured before application")
    if effect["status"] in {"helped", "no_effect", "worse"}:
        if not effect["method"] or not effect["metric"] or not effect["n_before"] or not effect["n_after"]:
            _fail(path, "measured effect needs method, metric, before and after samples")
    if proposal["change_type"] == "automation" and readiness["status"] == "prepared_verified":
        basis = _object(proposal.get("automation_basis"), f"{path}.automation_basis", {"steps", "frequency", "benefit", "existing_automations_checked", "exception_handling", "permission_scope", "stop_method"})
        _strings(basis["steps"], f"{path}.automation_basis.steps", nonempty=True)
        for key in ("frequency", "benefit", "existing_automations_checked", "exception_handling", "permission_scope", "stop_method"):
            _string(basis[key], f"{path}.automation_basis.{key}", nonempty=True)
    if "history_review" in proposal:
        _string(proposal["history_review"], f"{path}.history_review")
    sources = proposal["sources"]
    if not isinstance(sources, list) or not sources:
        _fail(f"{path}.sources", "expected nonempty array")
    seen_sources = set()
    for index, source in enumerate(sources):
        sp = f"{path}.sources[{index}]"
        source = _object(source, sp, {"session_id", "task_id", "source_sha256", "evidence"})
        sid = _string(source["session_id"], f"{sp}.session_id", nonempty=True)
        task_id = _string(source["task_id"], f"{sp}.task_id", nonempty=True)
        if candidate_session is not None and sid != candidate_session:
            _fail(sp, "per-session candidate cannot cite another session")
        if sid not in report_index:
            _fail(sp, "source session has no validated v2 report")
        task_bounds, records, sha = report_index[sid]
        if task_id not in task_bounds or source["source_sha256"] != sha:
            _fail(sp, "source task or frozen version mismatch")
        _evidence(source["evidence"], f"{sp}.evidence", records)
        start, end = task_bounds[task_id]
        if any(not start <= int(ref[1:]) <= end for ref in source["evidence"]):
            _fail(f"{sp}.evidence", "proposal evidence outside cited task boundary")
        source_key = (sid, task_id)
        if source_key in seen_sources:
            _fail(sp, "duplicate source task")
        seen_sources.add(source_key)


def validate_deep(report: object, *, session_id: str, source_sha256: str, source_records: int) -> None:
    """Reject malformed v2 Deep report or references outside a frozen source."""
    report = _object(report, "deep", {"kind", "schema_version", "session_id", "source_sha256", "tasks", "checks", "observations", "proposal_candidates", "unknowns"}, {"analysis_version", "previous_report"})
    if report["kind"] != "deep" or report["schema_version"] != 2 or report["session_id"] != session_id or report["source_sha256"] != source_sha256 or not SHA256.fullmatch(source_sha256):
        _fail("deep", "identity or source version mismatch")
    if type(source_records) is not int or source_records < 1:
        _fail("source_records", "expected positive frozen source length")
    if "analysis_version" in report:
        _string(report["analysis_version"], "deep.analysis_version", nonempty=True)
    if "previous_report" in report:
        _string(report["previous_report"], "deep.previous_report")
    tasks = report["tasks"]
    if not isinstance(tasks, list) or not tasks:
        _fail("deep.tasks", "at least one task required")
    ids = [_validate_task(task, f"deep.tasks[{index}]", source_records) for index, task in enumerate(tasks)]
    if len(ids) != len(set(ids)):
        _fail("deep.tasks", "duplicate task id")
    if any(task.get("continued_from", {}).get("session_id") == session_id for task in tasks):
        _fail("deep.tasks", "a task cannot continue from its own session")
    task_ids = set(ids)
    checks = report["checks"]
    if not isinstance(checks, list) or [item.get("id") if isinstance(item, dict) else None for item in checks] != list(CHECK_IDS):
        _fail("deep.checks", "all 13 checks required in catalogue order")
    for index, check in enumerate(checks):
        _validate_check(check, f"deep.checks[{index}]", source_records, task_ids)
    if not isinstance(report["observations"], list):
        _fail("deep.observations", "expected array")
    for index, observation in enumerate(report["observations"]):
        _validate_observation(observation, f"deep.observations[{index}]", source_records, task_ids)
    _strings(report["unknowns"], "deep.unknowns")
    check_d05 = checks[2]
    if check_d05["status"] == "suspected" and not any(claim["verification_at_claim"] in {"unverified", "contradicted"} for task in tasks for claim in task["claims"]):
        _fail("deep.checks[2]", "D05 signal needs an unverified or contradicted completion claim")
    candidates = report["proposal_candidates"]
    if not isinstance(candidates, list):
        _fail("deep.proposal_candidates", "expected array")
    report_index = {session_id: ({task["task_id"]: (task["start_line"], task["end_line"]) for task in tasks}, source_records, source_sha256)}
    seen_groups = set()
    for index, candidate in enumerate(candidates):
        _validate_proposal(candidate, f"deep.proposal_candidates[{index}]", report_index, candidate_session=session_id)
        if candidate["group_key"] in seen_groups:
            _fail("deep.proposal_candidates", "duplicate group key in one session")
        seen_groups.add(candidate["group_key"])


def validate_v2_deep(report: object, session: Mapping[str, object]) -> None:
    """Convenience entry point for a manifest session in the local watcher."""
    validate_deep(
        report,
        session_id=session["session_id"],
        source_sha256=session["source_sha256"],
        source_records=session["source_records"],
    )


def validate_semantic_review(review: object, *, session_id: str, candidate_sha256: str) -> None:
    """Bind an explicit semantic approval to one exact private report version."""
    review = _object(review, "review", {"session_id", "candidate_sha256", "verdict", "reviewer", "reviewed_at", "note", "checks"})
    if review["session_id"] != session_id or review["candidate_sha256"] != candidate_sha256 or not SHA256.fullmatch(candidate_sha256):
        _fail("review", "session or candidate version mismatch")
    if review["verdict"] != "approved":
        _fail("review.verdict", "explicit approval required")
    for field in ("reviewer", "reviewed_at", "note"):
        _string(review[field], f"review.{field}", nonempty=True)
    checks = _strings(review["checks"], "review.checks", nonempty=True)
    if len(checks) != len(REVIEW_CHECKS) or set(checks) != REVIEW_CHECKS:
        _fail("review.checks", "all independent semantic checks required exactly once")


def validate_registry(registry: object, reports: Mapping[str, tuple[dict, int]]) -> None:
    """Validate global proposals against already validated v2 reports.

    reports maps session_id to (report, frozen_source_records). This makes a
    proposal's task and source version independently checkable at promotion.
    """
    registry = _object(registry, "registry", {"kind", "schema_version", "proposals"}, {"generated_at"})
    if registry["kind"] != "proposal_registry" or registry["schema_version"] != 2:
        _fail("registry", "invalid identity")
    if "generated_at" in registry:
        _string(registry["generated_at"], "registry.generated_at", nonempty=True)
    if not isinstance(registry["proposals"], list):
        _fail("registry.proposals", "expected array")
    index = {}
    for sid, (report, records) in reports.items():
        validate_deep(report, session_id=sid, source_sha256=report["source_sha256"], source_records=records)
        index[sid] = ({task["task_id"]: (task["start_line"], task["end_line"]) for task in report["tasks"]}, records, report["source_sha256"])
    for sid, (report, _) in reports.items():
        for task in report["tasks"]:
            continuation = task.get("continued_from")
            if continuation is None:
                continue
            path = f"{sid}.{task['task_id']}.continued_from"
            peer = continuation["session_id"]
            if peer not in index or peer == sid:
                _fail(path, "previous session missing or same as current session")
            task_bounds, records, sha = index[peer]
            previous_task = continuation["task_id"]
            if previous_task not in task_bounds or continuation["source_sha256"] != sha:
                _fail(path, "previous task or frozen version mismatch")
            _evidence(continuation["previous_evidence"], f"{path}.previous_evidence", records)
            start, end = task_bounds[previous_task]
            if any(not start <= int(ref[1:]) <= end for ref in continuation["previous_evidence"]):
                _fail(path, "previous evidence outside cited task boundary")
        for check in report["checks"]:
            seen_peers = set()
            for source in check.get("peer_sources", []):
                path = f"{sid}.{check['id']}.peer_sources"
                peer = source["session_id"]
                if peer == sid or peer not in index:
                    _fail(path, "peer session missing or same as local session")
                task_bounds, records, sha = index[peer]
                if source["task_id"] not in task_bounds or source["source_sha256"] != sha:
                    _fail(path, "peer task or frozen version mismatch")
                peer_key = (peer, source["task_id"])
                if peer_key in seen_peers:
                    _fail(path, "duplicate peer task")
                seen_peers.add(peer_key)
                if check["id"] == "D22" and next(task for task in reports[peer][0]["tasks"] if task["task_id"] == source["task_id"])["outcome"] != "verified":
                    _fail(path, "D22 peer task must have verified outcome")
                _evidence(source["evidence"], f"{path}.evidence", records)
                start, end = task_bounds[source["task_id"]]
                if any(not start <= int(ref[1:]) <= end for ref in source["evidence"]):
                    _fail(path, "peer evidence outside cited task boundary")
    proposal_ids, group_keys = set(), set()
    for number, proposal in enumerate(registry["proposals"]):
        _validate_proposal(proposal, f"registry.proposals[{number}]", index)
        if proposal["proposal_id"] in proposal_ids or proposal["group_key"] in group_keys:
            _fail("registry.proposals", "duplicate proposal id or group key")
        proposal_ids.add(proposal["proposal_id"])
        group_keys.add(proposal["group_key"])
