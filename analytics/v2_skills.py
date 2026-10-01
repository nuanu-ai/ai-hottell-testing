"""Validate a private cross-session skill opportunity report.

This is a recommendation layer, not a retrospective D19/D25 verdict. Historical
skill availability is not inferred from today's inventory.
"""

from __future__ import annotations

import re
from collections.abc import Mapping


SHA256 = re.compile(r"^[0-9a-f]{64}$")
LINE = re.compile(r"^L[1-9][0-9]*$")
ID = re.compile(r"^[a-z0-9][a-z0-9-]{2,63}$")
KINDS = {"use_existing", "create", "install_candidate"}


def _require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def _text(value: object, name: str) -> str:
    _require(isinstance(value, str) and bool(value.strip()), f"{name}: nonempty text required")
    return value


def validate_skill_report(report: object, deep_reports: Mapping[str, dict], expected_inventory: dict | None = None) -> None:
    _require(isinstance(report, dict), "skill report: object required")
    _require(report.get("kind") == "skill_opportunities" and report.get("schema_version") == 1, "skill report: invalid identity")
    _text(report.get("analyzed_at"), "analyzed_at")
    inventory = report.get("inventory")
    _require(isinstance(inventory, dict), "inventory: object required")
    if expected_inventory is not None:
        _require(inventory == expected_inventory, "inventory: differs from recorded local snapshot")
    _text(inventory.get("snapshot_at"), "inventory.snapshot_at")
    items = inventory.get("items")
    _require(isinstance(items, list), "inventory.items: array required")
    names = set()
    for item in items:
        _require(isinstance(item, dict), "inventory item: object required")
        name = _text(item.get("name"), "inventory.name")
        _text(item.get("description"), "inventory.description")
        _text(item.get("path_class"), "inventory.path_class")
        _require(SHA256.fullmatch(str(item.get("description_sha256", ""))) is not None, "inventory: invalid description hash")
        _require(name not in names, "inventory: duplicate skill")
        names.add(name)
    corpus = report.get("corpus")
    _require(isinstance(corpus, list) and len(corpus) == len(deep_reports), "corpus: must cover all published Deep reports")
    seen_sessions = set()
    for entry in corpus:
        _require(isinstance(entry, dict), "corpus entry: object required")
        session_id = entry.get("session_id")
        _require(session_id in deep_reports and session_id not in seen_sessions, "corpus: unknown or duplicate session")
        _require(entry.get("source_sha256") == deep_reports[session_id]["source_sha256"], "corpus: source version mismatch")
        _require(SHA256.fullmatch(str(entry.get("deep_report_sha256", ""))) is not None, "corpus: Deep report hash required")
        seen_sessions.add(session_id)
    opportunities = report.get("opportunities")
    _require(isinstance(opportunities, list), "opportunities: array required")
    seen_ids = set()
    for item in opportunities:
        _require(isinstance(item, dict), "opportunity: object required")
        ident = item.get("id")
        _require(isinstance(ident, str) and ID.fullmatch(ident) is not None and ident not in seen_ids, "opportunity: invalid or duplicate ID")
        seen_ids.add(ident)
        kind = item.get("kind")
        _require(kind in KINDS, f"{ident}: invalid kind")
        for field in ("title", "recommendation", "why_skill", "alternative", "uncertainty", "verification"):
            _text(item.get(field), f"{ident}.{field}")
        _require(item.get("readiness") in {"hypothesis", "candidate", "prepared"}, f"{ident}: invalid readiness")
        sources = item.get("sources")
        _require(isinstance(sources, list) and sources, f"{ident}: sources required")
        distinct_sessions = set()
        for source in sources:
            _require(isinstance(source, dict), f"{ident}: invalid source")
            session_id, task_id = source.get("session_id"), source.get("task_id")
            deep = deep_reports.get(session_id)
            _require(deep is not None and source.get("source_sha256") == deep["source_sha256"], f"{ident}: source not in frozen corpus")
            tasks = {task["task_id"]: task for task in deep["tasks"]}
            _require(task_id in tasks, f"{ident}: unknown task")
            evidence = source.get("evidence")
            _require(isinstance(evidence, list) and evidence, f"{ident}: evidence required")
            for line in evidence:
                _require(isinstance(line, str) and LINE.fullmatch(line) is not None, f"{ident}: invalid source line")
                number = int(line[1:])
                _require(tasks[task_id]["start_line"] <= number <= tasks[task_id]["end_line"], f"{ident}: evidence outside task")
            distinct_sessions.add(session_id)
        if kind == "create":
            _require(len(distinct_sessions) >= 2, f"{ident}: custom skill needs cross-session evidence")
            _text(item.get("copy_prompt"), f"{ident}.copy_prompt")
            _require(not item.get("installed_skill") and not item.get("external_skill"), f"{ident}: unexpected skill target")
        elif kind == "use_existing":
            _require(item.get("installed_skill") in names, f"{ident}: skill absent from current inventory")
            _require(not item.get("copy_prompt") and not item.get("external_skill"), f"{ident}: unexpected target")
            _require(item.get("historical_availability") == "unknown", f"{ident}: cannot infer historical availability")
        else:
            external = item.get("external_skill")
            _require(isinstance(external, dict), f"{ident}: external skill source required")
            _text(external.get("name"), f"{ident}.external_skill.name")
            url = _text(external.get("url"), f"{ident}.external_skill.url")
            _require(url.startswith("https://"), f"{ident}: HTTPS source required")
            _require(external.get("review_status") == "source_review_required", f"{ident}: install requires source review")
            _require(not item.get("copy_prompt") and not item.get("installed_skill"), f"{ident}: unexpected target")
