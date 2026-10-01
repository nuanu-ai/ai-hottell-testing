"""Check source roles behind Deep task boundaries and completion claims.

Compaction summaries may describe earlier work, but their JSONL record is not
a user-role request. A task must begin at a user-role message (which can carry
the persisted, explicitly user-provided goal), and a completion claim must be
an assistant message. The structural contract alone cannot establish this.
"""

import json
from pathlib import Path


def validate_source_roles(report: dict, session: dict) -> None:
    starts = {task["start_line"] for task in report["tasks"]}
    goals = {int(ref[1:]) for task in report["tasks"] for ref in task["goal_evidence"]}
    claims = {claim["line"] for task in report["tasks"] for claim in task["claims"]}
    wanted = starts | goals | claims
    if not wanted:
        raise ValueError("Deep report has no source-bound tasks")
    roles = {}
    last_wanted = max(wanted)
    source = Path(session["source_path"])
    with source.open("rb") as stream:
        for number, raw in enumerate(stream, 1):
            if number > last_wanted:
                break
            if number in wanted:
                record = json.loads(raw)
                payload = record.get("payload") or {}
                roles[number] = (record.get("type"), payload.get("type"), payload.get("role"))
    user_message = ("response_item", "message", "user")
    assistant_message = ("response_item", "message", "assistant")
    for task in report["tasks"]:
        task_id = task["task_id"]
        if roles.get(task["start_line"]) != user_message:
            raise ValueError(f"{task_id}: task starts outside a user-role message")
        if not any(roles.get(int(ref[1:])) == user_message for ref in task["goal_evidence"]):
            raise ValueError(f"{task_id}: goal is not grounded in a user-role message")
        for claim in task["claims"]:
            if roles.get(claim["line"]) != assistant_message:
                raise ValueError(f"{task_id}: completion claim is not an assistant message")
