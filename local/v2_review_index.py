#!/usr/bin/env python3
"""Create a private navigation index for a frozen Codex session prefix.

The index is a reading aid, never evidence by itself. Its line numbers point to
the original JSONL, and truncated records must be opened there before review.
"""

import hashlib
import json
import os
import re
import sys
import tempfile
from pathlib import Path


SENSITIVE = (
    re.compile(r"(?i)\b(?:password|secret|token|api[_ -]?key|парол\w*)\b\s*[:=–—-]\s*(?:`[^`\n]+`|\"[^\"\n]+\"|'[^'\n]+'|\S+)"),
    re.compile(r"\bsk-[A-Za-z0-9_-]{20,}\b"),
    re.compile(r"\bAKIA[0-9A-Z]{16}\b"),
    # Opaque credentials can appear without a label. The index is only a
    # navigation aid, so redact long token-like values even at some cost to
    # preview detail; reviewers can inspect the original line privately.
    re.compile(r"(?<![A-Za-z0-9_])[A-Za-z0-9+/]{48,}={0,2}(?![A-Za-z0-9_])"),
    re.compile(r"(?<![A-Za-z0-9_])[A-Za-z0-9_-]{48,}(?![A-Za-z0-9_])"),
    re.compile(r"(?<![A-Za-z0-9_])[A-Za-z0-9_]{18,}(?![A-Za-z0-9_])"),
    re.compile(r"(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b"),
    re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
)
SENSITIVE_LINE = re.compile(r"(?i)\b(?:password|secret|token|api[_ -]?key|credential|парол\w*|секрет\w*|токен\w*|ключ\w*)\b")
REDACTION = "[скрыто в индексе; проверьте исходную строку]"


def redact_preview(value):
    # Markdown can put formatting between a credential label and its value
    # (for example, **Пароль:** `value`). Drop that entire preview line.
    redacted = "\n".join(REDACTION if SENSITIVE_LINE.search(line) else line for line in value.split("\n"))
    for pattern in SENSITIVE:
        redacted = pattern.sub(REDACTION, redacted)
    return redacted, redacted != value


def content_text(content):
    if not isinstance(content, list):
        return ""
    return "\n".join(
        part.get("text", "") for part in content
        if isinstance(part, dict) and isinstance(part.get("text"), str)
    )


def index_record(record, number):
    if record.get("type") != "response_item":
        return None
    payload = record.get("payload") or {}
    kind = payload.get("type")
    phase = payload.get("phase")
    text = ""
    limit = 0
    if kind == "message":
        role = payload.get("role")
        if role not in ("user", "assistant"):
            return None
        if role == "assistant" and phase not in ("final", "final_answer", "commentary"):
            return None
        category = f"{role}_{phase or 'message'}"
        text = content_text(payload.get("content"))
        limit = 4000 if role == "user" else 1800
    elif kind in ("function_call", "custom_tool_call"):
        category = "tool_call"
        text = str(payload.get("arguments") or payload.get("input") or "")
        limit = 300
    elif kind in ("function_call_output", "custom_tool_call_output"):
        category = "tool_result"
        text = str(payload.get("output") or "")
        limit = 300
    else:
        return None
    preview, redacted = redact_preview(text)
    return {
        "line": number,
        "at": record.get("timestamp"),
        "category": category,
        "tool": payload.get("name") if category == "tool_call" else None,
        "call_id": payload.get("call_id") if category in ("tool_call", "tool_result") else None,
        "text": preview[:limit],
        "text_length": len(text),
        "truncated": len(text) > limit,
        "redacted": redacted,
    }


def build_index(session, output_root):
    sid = session["session_id"]
    source_path = Path(session["source_path"])
    output_root.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(output_root, 0o700)
    fd, temp_path = tempfile.mkstemp(prefix=f".{sid}-", suffix=".jsonl", dir=output_root)
    digest = hashlib.sha256()
    count = 0
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w") as output, source_path.open("rb") as source:
            for number in range(1, session["source_records"] + 1):
                raw = source.readline()
                if not raw:
                    raise ValueError(f"{sid}: source shorter than frozen prefix")
                digest.update(raw)
                record = json.loads(raw)
                item = index_record(record, number)
                if item is not None:
                    output.write(json.dumps(item, ensure_ascii=False, separators=(",", ":")) + "\n")
                    count += 1
        if digest.hexdigest() != session["source_sha256"]:
            raise ValueError(f"{sid}: source hash changed")
        os.replace(temp_path, output_root / f"{sid}.jsonl")
    finally:
        if os.path.exists(temp_path):
            os.unlink(temp_path)
    return count


def main():
    if len(sys.argv) not in (2, 3):
        raise SystemExit("usage: v2_review_index.py /absolute/path/to/reports [session_id]")
    root = Path(sys.argv[1])
    if not root.is_absolute():
        raise ValueError("report path must be absolute")
    sessions = json.loads((root / "telemetry" / "manifest.json").read_text())["sessions"]
    requested = sys.argv[2] if len(sys.argv) == 3 else None
    selected = [s for s in sessions if requested is None or s["session_id"] == requested]
    if not selected:
        raise ValueError("session is absent from frozen manifest")
    for session in selected:
        count = build_index(session, root / "v2" / "review-index")
        print(session["session_id"], count)


if __name__ == "__main__":
    main()
