#!/usr/bin/env python3
"""Rebuild private retrospective telemetry from frozen Codex rollout prefixes.

Usage: python3 dashboard/rebuild_telemetry.py /absolute/path/to/local-data/reports
Writes candidates to reports/rebuild/<session_id>/telemetry.json. It never
copies message bodies, tool arguments, or tool results into the report.
"""

import collections
import hashlib
import json
import os
import sys
import tempfile
from pathlib import Path


TOKEN_KEYS = ("input_tokens", "cached_input_tokens", "output_tokens", "total_tokens")


def observed_usage(snapshots):
    """Sum final cumulative counters per reset segment; ignore duplicate polls."""
    totals = collections.Counter()
    last = None
    duplicate_polls = resets = valid = 0
    for snapshot in snapshots:
        if not isinstance(snapshot, dict) or not isinstance(snapshot.get("total_tokens"), int):
            continue
        current = {key: snapshot.get(key, 0) for key in TOKEN_KEYS}
        if any(not isinstance(value, int) or value < 0 for value in current.values()):
            continue
        valid += 1
        if last is not None and current["total_tokens"] < last["total_tokens"]:
            totals.update(last)
            resets += 1
        elif last is not None and current == last:
            duplicate_polls += 1
        last = current
    if last is not None:
        totals.update(last)
    return (dict(totals) if valid else None), valid, duplicate_polls, resets


def metric(value, unit, detail=""):
    result = {
        "value": value,
        "unit": unit,
        "source": "transcript" if value is not None else None,
        "provenance": "reconstructed" if value is not None else "missing",
    }
    if detail:
        result["detail"] = detail
    return result


def rebuild(session, output_root, *, retrospective=True):
    sid = session["session_id"]
    digest = hashlib.sha256()
    counts = collections.Counter()
    events = []
    snapshots = []
    first = last = None
    path = Path(session["source_path"])
    with path.open("rb") as source:
        for line_number in range(1, session["source_records"] + 1):
            raw = source.readline()
            if not raw:
                raise ValueError(f"{sid}: source ended before frozen prefix")
            digest.update(raw)
            record = json.loads(raw)
            at = record.get("timestamp")
            if at:
                first = first or at
                last = at
            payload = record.get("payload") or {}
            record_type = record.get("type")
            subtype = payload.get("type")
            kind = tool = None
            if record_type == "event_msg" and subtype == "task_started":
                kind = "turn_started"
            elif record_type == "event_msg" and subtype == "task_complete":
                kind = "turn_completed"
            elif record_type == "response_item" and subtype == "message":
                if payload.get("role") == "user":
                    kind = "user_message"
                elif payload.get("role") == "assistant":
                    kind = "assistant_message"
            elif record_type == "response_item" and subtype in ("function_call", "custom_tool_call"):
                kind = "tool_call"
                tool = payload.get("name")
            elif record_type == "response_item" and subtype in ("function_call_output", "custom_tool_call_output"):
                kind = "tool_result"
            elif record_type == "compacted":
                kind = "context_compaction"
            if kind:
                counts[kind] += 1
                event = {
                    "id": f"{sid}:L{line_number}", "at": at, "kind": kind,
                    "source": "transcript", "provenance": "reconstructed",
                    "evidence": {"source": "local_transcript", "line": line_number},
                }
                if tool:
                    event["tool"] = tool
                events.append(event)
            if record_type == "event_msg" and subtype == "token_count":
                snapshots.append(((payload.get("info") or {}).get("total_token_usage")))
    if digest.hexdigest() != session["source_sha256"]:
        raise ValueError(f"{sid}: frozen source hash changed")

    usage, valid, duplicates, resets = observed_usage(snapshots)
    token_detail = (
        f"Observed final cumulative counters across {resets + 1} counter segments; "
        f"{valid} snapshots, {duplicates} duplicate polls. Reconstructed from the "
        "frozen transcript prefix; not billed usage or OTel."
        if usage else "No usable cumulative token counter in the frozen prefix."
    )
    metrics = {
        "turns_started": metric(counts["turn_started"], "turns"),
        "turns_completed": metric(counts["turn_completed"], "turns"),
        "user_messages": metric(counts["user_message"], "messages", "Counts records with role=user, including injected context and automated prompts; not a count of human requests."),
        "assistant_messages": metric(counts["assistant_message"], "messages"),
        "tool_calls": metric(counts["tool_call"], "calls"),
        "tool_results": metric(counts["tool_result"], "results"),
        "context_compactions": metric(counts["context_compaction"], "events"),
        "token_input": metric(usage["input_tokens"] if usage else None, "tokens", token_detail),
        "token_cached_input": metric(usage["cached_input_tokens"] if usage else None, "tokens", token_detail),
        "token_output": metric(usage["output_tokens"] if usage else None, "tokens", token_detail),
        "token_total": metric(usage["total_tokens"] if usage else None, "tokens", token_detail),
        "cost_usd": metric(None, "USD"),
        "api_latency_ms": metric(None, "ms"),
        "quality_score": metric(None, "score"),
    }
    capture_coverage = ({
        "hook": {"status": "missing", "detail": "No retained Hooks for the historical prefix; later recorded Hooks are read separately from the local store."},
        "otel": {"status": "missing", "detail": "No historical session-addressable OTel in this retrospective source; current-store records are read separately."},
    } if retrospective else {
        "hook": {"status": "separate_store", "detail": "The frozen Codex source contains no Hook records. Recorded Hooks, if any, are queried by exact session ID from the local store below."},
        "otel": {"status": "separate_store", "detail": "The frozen Codex source contains no native OTel records. Recorded OTel, if any, is queried by exact session ID from the local store below."},
    })
    report = {
        "kind": "telemetry", "schema_version": 1, "session_id": sid,
        "thread_id": sid, "agent": "codex", "source_url": f"codex://threads/{sid}",
        "availability": "available",
        "source_coverage": {
            **capture_coverage,
            "transcript": {"status": "available", "detail": f"Frozen local Codex rollout prefix, {session['source_records']} JSONL records; content remains private."},
            "app": {"status": "available", "detail": "The provided Codex deep link resolved to this exact thread."},
        },
        "events": events,
        "metrics": metrics,
        "gaps": [
            "Hooks and OTel are not reconstructed from transcript events; recorded streams are checked separately by exact session ID.",
            "Tool arguments, outputs and message bodies are deliberately absent from this technical report.",
            "Nested CLI and subagent activity inside tool outputs is not counted as separate top-level events.",
            "Billed cost, API latency, quality and human time saved are unknown.",
            "Cumulative token counters may omit intervals around resets and are not a task-class baseline or exact billable usage.",
            "Records with role=user can include service context and automated prompts; human message count is unknown.",
        ],
        "period": {"first_recorded_at": first, "last_recorded_at": last},
        "source": {"type": "local_codex_rollout", "path": str(path),
                   "sha256": digest.hexdigest(), "records": session["source_records"]},
    }
    folder = output_root / sid
    folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(folder, 0o700)
    fd, temp = tempfile.mkstemp(prefix=".telemetry-", suffix=".json", dir=folder)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w") as target:
            json.dump(report, target, ensure_ascii=False, separators=(",", ":"))
            target.write("\n")
        os.replace(temp, folder / "telemetry.json")
    finally:
        if os.path.exists(temp):
            os.unlink(temp)
    return len(events), valid, duplicates, resets


def main():
    if len(sys.argv) != 2 or not Path(sys.argv[1]).is_absolute():
        raise SystemExit("usage: rebuild_telemetry.py /absolute/path/to/local-data/reports")
    root = Path(sys.argv[1]).resolve()
    sessions = json.loads((root / "telemetry" / "manifest.json").read_text())["sessions"]
    if len(sessions) != len({s["session_id"] for s in sessions}):
        raise ValueError("duplicate session in manifest")
    for session in sessions:
        count, snapshots, duplicates, resets = rebuild(session, root / "rebuild")
        print(session["session_id"], count, snapshots, duplicates, resets)


if __name__ == "__main__":
    main()
