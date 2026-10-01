#!/usr/bin/env python3
"""Build the «live» variant of the hottell UI v3 dataset from the local ClickHouse stand only.

    python3 ui/builder/live.py [--clickhouse http://127.0.0.1:8123] [--database otel]
                               [--since 2026-09-30T00:00:00Z] [--out local-data/ui-live]

Sources (nothing else is read — no rollouts, Deep reports or tests):
  * otel_logs, ServiceName='agent-hooks'         — hottell Hooks of Codex and Claude Code;
  * otel_logs, ServiceName='claude-code-desktop' — Claude Code native OTel events;
  * otel_metrics_sum, MetricName 'claude_code.*' — Claude Code native OTel metrics;
  * otel_logs of Codex services                  — only to report what is (not) linked to sessions.

Output follows ui/CONTRACT.md (same UI), plus "variant": "live". Timeline `line` is the event's index
within the session (1..N): there is no transcript line here. Standard library only.
"""

from __future__ import annotations

import argparse
import bisect
import collections
import datetime as dt
import functools
import hashlib
import json
import os
import re
import socket
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parent.parent
if str(HERE) not in sys.path:
    sys.path.insert(0, str(HERE))

import telemetry as T  # noqa: E402

TITLE = "Новые сессии · Hooks + OTel"
HOOKS_SVC = "agent-hooks"
CLAUDE_SVC = "claude-code-desktop"
CODEX_SVCS = ("Codex Desktop", "codex-app-server", "codex_cli_rs")
SYNTHETIC_SID = re.compile(r"^hottell-.*-check-")
PARTIAL_AFTER_H = 1.0
EDIT_TOOLS = {"apply_patch", "Edit", "Write", "MultiEdit", "NotebookEdit"}
WAIT_TOOLS = {"request_user_input", "request_user_input_async", "AskUserQuestion"}
STOP_MIN_S = 5          # ответ быстрее — вопрос на самом деле никто не ждал (отказ, автоответ)
POLL_TOOLS = {"write_stdin", "collaboration.wait_agent", "clock.sleep", "wait", "sleep"}
CODEX_NAMESPACES = ("collaboration", "clock", "image_gen", "web")   # glued to the tool name in Codex hooks
# Сессии явного служебного происхождения — интерфейс по умолчанию их скрывает. Сомнительные (короткие, без реплик)
# остаются видимыми: короткая сессия бывает обычной работой, отсутствие реплик — неполным сбором.
SYSTEM_PROMPTS = (
    (re.compile(r"^# Overview Generate \d+ to \d+ hyperpersonalized suggestions"), "подсказки Codex Desktop"),
    (re.compile(r"^## Memory Writing Agent"), "память Codex"),
    (re.compile(r"^You are an expert at upholding safety and compliance standards for Codex ambient suggestions"),
     "проверка подсказок Codex"),
)
SYSTEM_CWD = re.compile(r"/\.codex/memories(?:/|$)")     # рабочая папка самого Codex, не проект
AUTOMATION_PROMPT = re.compile(r"^<heartbeat>")


class ClickHouseError(RuntimeError):
    pass


# --------------------------------------------------------------------------- query layer


class ClickHouse:
    """Minimal ClickHouse HTTP client: POST SQL, FORMAT JSONEachRow, retry when the server runs out of memory."""

    def __init__(self, url="http://127.0.0.1:8123", database="otel", timeout=120, retries=5):
        self.url = url.rstrip("/")
        self.database = database
        self.timeout = timeout
        self.retries = retries

    def __call__(self, name, sql):
        params = urllib.parse.urlencode({"database": self.database, "max_threads": 1, "max_block_size": 32,
                                          "output_format_json_quote_64bit_integers": 0})
        body = (sql.strip() + "\nFORMAT JSONEachRow").encode("utf-8")
        last = None
        for attempt in range(self.retries):
            try:
                req = urllib.request.Request(f"{self.url}/?{params}", data=body, method="POST")
                with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                    text = resp.read().decode("utf-8", "replace")
                rows, err = parse_rows(text)
                if err is None:
                    return rows
                last = err
            except urllib.error.HTTPError as exc:
                last = exc.read().decode("utf-8", "replace")[:600]
                exc.close()
            except (urllib.error.URLError, OSError) as exc:
                raise ClickHouseError(f"{name}: ClickHouse недоступен ({exc})") from exc
            if "MEMORY_LIMIT_EXCEEDED" not in last and "memory limit" not in last.lower():
                break
            time.sleep(1.5 * (attempt + 1))
        raise ClickHouseError(f"{name}: {last}")


def parse_rows(text):
    """JSONEachRow text -> (rows, error). ClickHouse reports mid-stream failures as a row {"exception": ...}."""
    rows = []
    for ln in text.splitlines():
        ln = ln.strip()
        if not ln:
            continue
        if ln.startswith("Code:"):
            return rows, ln
        row = json.loads(ln)
        if isinstance(row, dict) and "exception" in row and len(row) == 1:
            return rows, str(row["exception"])
        rows.append(row)
    return rows, None


_SINCE = re.compile(r"^\d{4}-\d\d-\d\d(?:[T ]\d\d:\d\d(?::\d\d(?:\.\d+)?)?)?Z?$")


def _time_cond(col, since, until=None):
    parts = []
    for op, value in ((">=", since), ("<", until)):
        if value:
            if not _SINCE.match(value):
                raise ValueError(f"неверная дата: {value!r}")
            parts.append(f"{col} {op} parseDateTime64BestEffort('{value}', 9)")
    return " AND ".join(parts) or "1"


SQL = {
    "bounds": """SELECT toString(min(Timestamp)) lo, toString(max(Timestamp)) hi, count() n
        FROM otel_logs WHERE ServiceName = 'agent-hooks' AND {time}""",
    "hooks": """SELECT toString(Timestamp) ts, Body ev, LogAttributes['agent'] agent, LogAttributes['session_id'] sid,
        LogAttributes['turn_id'] turn_id, LogAttributes['prompt_id'] prompt_id, LogAttributes['tool_name'] tool,
        LogAttributes['tool_use_id'] tuid, substringUTF8(LogAttributes['tool_input'], 1, 4000) tin,
        toString(cityHash64(LogAttributes['tool_input'])) tin_hash,
        substringUTF8(LogAttributes['tool_response'], 1, 1500) resp_head,
        substringUTF8(LogAttributes['tool_response'],
                      toUInt64(greatest(1, toInt64(lengthUTF8(LogAttributes['tool_response'])) - 400)), 401) resp_tail,
        length(LogAttributes['tool_response']) resp_len,
        toString(cityHash64(replaceRegexpOne(LogAttributes['tool_response'],
            '^(?:(?:Chunk ID|Wall time|Process exited with code|Original token count)[^\\n]*\\n)*(?:Output:\\n)?', ''))) resp_hash,
        LogAttributes['duration_ms'] dur,
        LogAttributes['permission_mode'] pm, LogAttributes['agent_type'] atype, LogAttributes['agent_id'] aid,
        LogAttributes['cwd'] cwd, LogAttributes['model'] model, LogAttributes['mcp_server'] mcp_server,
        substringUTF8(LogAttributes['prompt'], 1, 4000) prompt,
        substringUTF8(LogAttributes['last_assistant_message'], 1, 2000) last_msg,
        LogAttributes['session_title'] title, LogAttributes['source'] source, LogAttributes['reason'] reason,
        LogAttributes['trigger'] trig,
        if(Body = 'agent.hook.SessionStart', LogAttributes['hottell.skill_snapshot'], '') snapshot,
        LogAttributes['hottell.enrich_status'] henrich, LogAttributes['hottell.exit_code'] hx,
        LogAttributes['hottell.tool_status'] hstatus, LogAttributes['hottell.tool_duration_ms'] hdur,
        LogAttributes['hottell.turn.responses'] t_resp, LogAttributes['hottell.turn.input_tokens'] t_in,
        LogAttributes['hottell.turn.cached_input_tokens'] t_cached, LogAttributes['hottell.turn.output_tokens'] t_out,
        LogAttributes['hottell.turn.reasoning_output_tokens'] t_reas, LogAttributes['hottell.turn.model'] t_model,
        LogAttributes['hottell.turn.duration_ms'] t_dur
        FROM otel_logs WHERE ServiceName = 'agent-hooks' AND {time}""",
    "claude_logs": """SELECT toString(Timestamp) ts, LogAttributes['event.name'] e, LogAttributes['session.id'] sid,
        LogAttributes['prompt.id'] prompt_id, LogAttributes['model'] model, LogAttributes['input_tokens'] inp,
        LogAttributes['output_tokens'] outp, LogAttributes['cache_read_tokens'] cread,
        LogAttributes['cache_creation_tokens'] ccreate, LogAttributes['cost_usd'] cost, LogAttributes['duration_ms'] dur,
        LogAttributes['query_source'] qsrc, LogAttributes['tool_name'] tool, LogAttributes['tool_use_id'] tuid,
        LogAttributes['success'] success, substringUTF8(LogAttributes['error'], 1, 300) err,
        LogAttributes['decision'] decision, LogAttributes['source'] dsource, LogAttributes['status_code'] status,
        LogAttributes['agent_type'] atype
        FROM otel_logs WHERE ServiceName = 'claude-code-desktop'
          AND LogAttributes['event.name'] IN ('api_request', 'api_error', 'tool_result', 'tool_decision',
                                              'user_prompt', 'subagent_completed') AND {time}""",
    "claude_metrics": """SELECT MetricName metric, Attributes['session.id'] sid, Attributes['type'] type,
        Attributes['model'] model, toString(TimeUnix) ts, Value value
        FROM otel_metrics_sum WHERE MetricName LIKE 'claude_code.%' AND {mtime}""",
    "codex_otel": """SELECT ServiceName svc, LogAttributes['event.name'] e,
        substringUTF8(LogAttributes['endpoint'], 1, 80) endpoint, LogAttributes['conversation.id'] != '' linked,
        count() n, uniqExact(LogAttributes['conversation.id']) convs
        FROM otel_logs WHERE ServiceName IN ('Codex Desktop', 'codex-app-server', 'codex_cli_rs') AND {time}
        GROUP BY svc, e, endpoint, linked""",
    "codex_sse": """SELECT toString(Timestamp) ts, LogAttributes['conversation.id'] sid, LogAttributes['event.kind'] kind,
        LogAttributes['model'] model, LogAttributes['input_token_count'] inp, LogAttributes['cached_token_count'] cached,
        LogAttributes['output_token_count'] outp, LogAttributes['reasoning_token_count'] reas
        FROM otel_logs WHERE ServiceName IN ('Codex Desktop', 'codex-app-server', 'codex_cli_rs')
          AND LogAttributes['event.name'] = 'codex.sse_event' AND LogAttributes['conversation.id'] != '' AND {time}""",
}


def fetch(query, since=None, until=None, window_h=4):
    """Run all queries through ``query(name, sql) -> rows``. Hook rows are fetched in time windows
    so a single query never has to hold all tool payloads in the server's memory."""
    tc = _time_cond("Timestamp", since, until)
    mc = _time_cond("TimeUnix", since, until)
    out = {}
    bounds = query("bounds", SQL["bounds"].format(time=tc))
    hooks = []
    b = bounds[0] if bounds else {}
    lo, hi = parse_ch_ts(b.get("lo")), parse_ch_ts(b.get("hi"))
    if b.get("n") and lo and hi and lo.year > 1970:
        x = lo
        while x <= hi:
            y = x + dt.timedelta(hours=window_h)
            cond = _time_cond("Timestamp", _iso_plain(x), _iso_plain(y))
            if since:
                cond += " AND " + _time_cond("Timestamp", since)
            hooks.extend(query("hooks", SQL["hooks"].format(time=cond)))
            x = y
    out["hooks"] = hooks
    for name in ("claude_logs", "codex_otel", "codex_sse"):
        out[name] = query(name, SQL[name].format(time=tc))
    out["claude_metrics"] = query("claude_metrics", SQL["claude_metrics"].format(mtime=mc))
    return out


def _iso_plain(d):
    return d.strftime("%Y-%m-%dT%H:%M:%S.%f")


def parse_ch_ts(value):
    """'2026-09-30 02:56:58.448339000' (UTC) -> aware datetime."""
    if not value:
        return None
    s = str(value).strip().replace("T", " ").rstrip("Z")
    if "." in s:
        head, frac = s.split(".", 1)
        s = head + "." + (frac + "000000")[:6]
    try:
        return dt.datetime.fromisoformat(s).replace(tzinfo=dt.timezone.utc)
    except ValueError:
        return None


# --------------------------------------------------------------------------- small helpers


def uuid7_time(sid):
    h = (sid or "").replace("-", "")
    if len(h) != 32 or h[12] != "7":
        return None
    try:
        return dt.datetime.fromtimestamp(int(h[:12], 16) / 1000.0, dt.timezone.utc)
    except (ValueError, OverflowError, OSError):
        return None


def display_tool(name):
    """Codex glues namespace and tool (collaborationspawn_agent) — show them as namespace.tool."""
    for ns in CODEX_NAMESPACES:
        if name.startswith(ns) and len(name) > len(ns) and not name.startswith(ns + ".") and not name.startswith("mcp__"):
            return f"{ns}.{name[len(ns):].lstrip('_')}"
    return name


def mcp_server_of(tool, attr=""):
    """MCP server of a call: Claude sends mcp_server as JSON {"name": …, "source": …}; Codex only has mcp__<server>__<tool>."""
    if attr:
        obj = _json(attr) if attr.lstrip().startswith("{") else None
        if isinstance(obj, dict) and obj.get("name"):
            return str(obj["name"])
        return attr
    if tool.startswith("mcp__"):
        parts = tool.split("__")
        return parts[1] if len(parts) > 2 else None
    return None


def _json(text):
    try:
        v = json.loads(text)
        return v
    except (ValueError, TypeError):
        return None


def _num(v):
    try:
        return float(v)
    except (TypeError, ValueError):
        return None


def _int(v):
    f = _num(v)
    return int(f) if f is not None else None


def _plain(e):
    """Доказательство без внутреннего поля _cost."""
    return {k: v for k, v in e.items() if k != "_cost"}


_PATCH_FILE = re.compile(r"\*\*\* (?:Add|Update|Delete) File: ([^\s\\]+)")
_EXIT_HEAD = re.compile(r"^(?:Exit code:\s*(-?\d+)|Chunk ID:[^\n]*\n(?:Wall time:[^\n]*\n)?Process exited with code\s*(-?\d+))")
_ISERROR_TAIL = re.compile(r'"isError"\s*:\s*(true|false)\s*\}\s*$')
_GIT_COMMIT_OUT = re.compile(r"^\[[^\]\n]+ [0-9a-f]{7,40}\]", re.M)
_PR_URL = re.compile(r"https://github\.com/[^\s/]+/[^\s/]+/pull/\d+")
_STAT = re.compile(r"(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?")
# служебные строки Codex перед выводом команды и ошибка чтения в первой строке вывода (без кода выхода исход «done»)
_CODEX_PREFIX = re.compile(r"^(?:(?:Exit code|Chunk ID|Wall time|Process exited with code|Original token count)[^\n]*\n)*"
                           r"(?:Output:\n)?")
_READ_ERROR = re.compile(r"(?:cat|sed|head|tail|nl|less|bat): [^\n]*?: "
                         r"(?:No such file or directory|Permission denied|Is a directory|Operation not permitted)")


def tool_summary(tool, tin, limit=140):
    """Short text for a tool call: command / file / pattern / url / skill / question."""
    args = _json(tin) if tin else None
    if not isinstance(args, dict):
        return T.clean(f"{tool} · {tin or ''}", limit)
    if tool == "apply_patch":
        files = _PATCH_FILE.findall(str(args.get("command") or args.get("patch") or tin))
        more = f" +{len(files) - 1}" if len(files) > 1 else ""
        return T.clean("apply_patch · " + (os.path.basename(files[0]) if files else ""), limit - len(more)) + more
    if tool in WAIT_TOOLS:
        qs = args.get("questions") or []
        q = qs[0] if qs and isinstance(qs[0], dict) else {}
        return T.clean("вопрос вам · " + str(q.get("title") or q.get("question") or ""), limit)
    for key in ("command", "cmd"):
        if isinstance(args.get(key), str):
            return T.clean(args[key], limit)
    if tool == "Skill":
        return T.clean(f"skill · {args.get('skill') or args.get('name') or ''}", limit)
    if tool == "Agent":
        return T.clean(f"Agent → {args.get('subagent_type') or ''} · {args.get('description') or ''}", limit)
    for key in ("file_path", "path", "notebook_path"):
        if isinstance(args.get(key), str):
            return T.clean(f"{display_tool(tool)} · {args[key]}", limit)
    if isinstance(args.get("pattern"), str):
        where = f" в {args['path']}" if isinstance(args.get("path"), str) else ""
        return T.clean(f"{display_tool(tool)} · {args['pattern']}{where}", limit)
    for key in ("url", "title", "task_name", "target", "query", "description", "skill"):
        if isinstance(args.get(key), str) and args[key]:
            return T.clean(f"{display_tool(tool)} · {args[key]}", limit)
    if isinstance(args.get("code"), str):
        first = next((ln for ln in args["code"].splitlines() if ln.strip()), "")
        return T.clean(f"{display_tool(tool)} · {first}", limit)
    return T.clean(display_tool(tool), limit)


def read_targets(cmd, cwd=None):
    """Files a shell command reads (cat/sed -n/head/tail/nl/less) with the read span."""
    out = []
    span = T.read_span(cmd or "")
    for seg in T.split_segments(cmd or ""):
        toks = T._strip_prefix(seg)
        if not toks:
            continue
        prog = os.path.basename(toks[0])
        if prog not in ("cat", "sed", "head", "tail", "nl", "less", "bat"):
            continue
        pos = [t for t in toks[1:] if not t.startswith("-")]
        if prog == "sed":
            pos = pos[1:]
        for p in pos:
            if re.fullmatch(r"\d+", p) or not re.search(r"[\w.]", p) or p in ("|", ">"):
                continue
            path = p if os.path.isabs(p) or not cwd else os.path.normpath(os.path.join(cwd, p))
            out.append((path, span))
    return out


_FRONT_NAME = re.compile(r"name:\s*['\"]?([^'\"]+?)['\"]?\s*$")


@functools.lru_cache(maxsize=None)
def skill_name(path):
    """Имя skill: поле name фронтматтера SKILL.md (между первой строкой --- и следующей ---; name: в теле не в счёт);
    иначе папка, а для общих имён (skill, skills) — папка выше. Каждый файл читается один раз."""
    try:
        with open(os.path.expanduser(path), encoding="utf-8") as fh:
            lines = fh.read(2048).splitlines()
    except (OSError, ValueError):                                   # нет файла или не UTF-8
        lines = []
    if lines and lines[0].strip() == "---":
        for ln in lines[1:]:
            if ln.strip() == "---":
                break
            m = _FRONT_NAME.match(ln)
            if m:
                return m.group(1).strip()
    d = os.path.dirname(path)
    name = os.path.basename(d)
    if name.lower() in ("skill", "skills", ""):
        name = os.path.basename(os.path.dirname(d)) or name
    return name or path


def read_call_targets(c, cwd):
    """(файл, место), которые прочитал вызов: Claude Read (offset/limit) или cat/sed -n/head/tail в shell."""
    if c["tool"] == "Read" and isinstance(c["args"].get("file_path"), str):
        off, lim = c["args"].get("offset"), c["args"].get("limit")
        span = f"строки {off}–{(off or 0) + lim}" if isinstance(off, int) and isinstance(lim, int) else (
            f"первые {lim}" if isinstance(lim, int) else "весь файл")
        return [(c["args"]["file_path"], span)]
    return read_targets(c["cmd"], cwd) if c["cmd"] else []


def edited_paths(c, cwd):
    """Файлы, которые меняет вызов: Edit/Write (file_path) и apply_patch (*** Update File: …)."""
    if c["tool"] in EDIT_TOOLS - {"apply_patch"}:
        p = c["args"].get("file_path") or c["args"].get("notebook_path")
        return [p] if isinstance(p, str) else []
    if c["tool"] == "apply_patch":
        files = _PATCH_FILE.findall(str(c["args"].get("command") or c["args"].get("patch") or c["tin"]))
        return [f if os.path.isabs(f) or not cwd else os.path.normpath(os.path.join(cwd, f)) for f in files]
    return []


def read_signature(c):
    """Хэш записанного ответа чтения. None — результата нет: содержимое неизвестно, равенство утверждать нельзя."""
    if c["post_at"] is None or not c.get("resp_len"):
        return None
    return c.get("resp_hash")


def read_failed(c):
    """Чтение с ошибкой — исход error или ошибка cat/sed/head… в первой строке ответа: содержимого агент не видел."""
    return c["state"] == "error" or bool(_READ_ERROR.match(_CODEX_PREFIX.sub("", c["resp_head"].lstrip(), count=1)))


def reread_runs(items, cuts):
    """items — [(вызов, хэш)] одного агента и места файла по времени, только с известным содержимым;
    cuts — сжатия и правки файла. Серия — подряд одинаковое содержимое без сжатия и правки между чтениями."""
    runs, run, seg, last = [], [], None, None
    for c, sig in items:
        k = bisect.bisect_right(cuts, c["at"])
        if run and (k != seg or sig != last):
            runs.append(run)
            run = []
        run.append(c)
        seg, last = k, sig
    if run:
        runs.append(run)
    return [r for r in runs if len(r) >= T.REREAD_MIN]


# --------------------------------------------------------------------------- per-session assembly


def group_sessions(hook_rows):
    """(agent, session_id) -> rows sorted by time; plus counts of what was dropped and why."""
    groups = collections.defaultdict(list)
    dropped = collections.Counter()
    for r in hook_rows:
        agent, sid = r.get("agent") or "", r.get("sid") or ""
        if agent in ("", "install"):
            dropped["install"] += 1
            continue
        if not sid:
            dropped["без session_id"] += 1
            continue
        if SYNTHETIC_SID.match(sid):
            dropped["проверки hottell"] += 1
            continue
        r = dict(r)
        r["at"] = parse_ch_ts(r.get("ts"))
        if r["at"] is None:
            dropped["без времени"] += 1
            continue
        groups[(agent, sid)].append(r)
    kept, empty = {}, []
    for key, rows in groups.items():
        rows.sort(key=lambda x: x["at"])
        evs = {x["ev"] for x in rows}
        if evs & {"agent.hook.UserPromptSubmit", "agent.hook.PreToolUse", "agent.hook.PostToolUse"}:
            kept[key] = rows
        else:
            empty.append(key)
    return kept, dropped, empty


def call_state(c):
    """(state, note) of one tool call from its Post payload / Claude tool_result.
    States: error, success, done (finished, but the payload carries no success/error signal), unknown (no result)."""
    if c.get("cc_success") is not None:
        if c["cc_success"]:
            return "success", None
        return "error", T.clean(c.get("cc_err") or "tool_result: success=false", 160)
    if c.get("post_at") is None:
        return "unknown", "нет PostToolUse"
    # Код выхода и статус, которые дрейнер hottell взял из журнала сессии Codex, точнее разбора текста ответа.
    if c.get("hx") is not None:
        return ("success", None) if c["hx"] == 0 else ("error", f"exit {c['hx']} · из журнала сессии")
    if c.get("hstatus") in ("failed", "error", "declined", "rejected", "cancelled"):
        return "error", f"{c['hstatus']} · из журнала сессии"
    head = (c.get("resp_head") or "").lstrip()
    tail = c.get("resp_tail") or ""
    m = _EXIT_HEAD.match(head)
    if m:
        code = int(m.group(1) or m.group(2))
        if code != 0:
            detail = head.split("Output:", 1)[-1] if "Output:" in head else ""
            return "error", T.clean(f"exit {code}" + (f" · {T._error_line(detail, 110)}" if detail.strip() else ""), 160)
        return "success", None
    m = _ISERROR_TAIL.search(tail)
    if m:
        return ("error", T.clean(T._first_line(head, 120) or "isError: true", 160)) if m.group(1) == "true" else ("success", None)
    small = _json(head) if (c.get("resp_len") or 0) <= 1500 else None
    if isinstance(small, dict):
        if small.get("isError") is True or small.get("is_error") is True:
            return "error", T.clean(json.dumps(small, ensure_ascii=False), 160)
        if small.get("interrupted") is True:
            return "error", "прервано"
        err = small.get("error")
        if err not in (None, "", False, {}):
            return "error", T.clean(f"error: {err}", 160)
        if "isError" in small or small.get("accepted") is True or "timed_out" in small or c["agent"] == "claude":
            return "success", None
    if c["agent"] == "claude":
        return "success", None      # Claude sends PostToolUse only for completed tools; failures come via tool_result
    if c.get("hstatus") == "completed":
        return "success", None
    return "done", None


def build_session(agent, sid, rows, claude=None, metrics=None, sse=None, now=None):
    """All contract fields of one session plus internals for aggregates, from its hook rows and linked OTel."""
    claude = claude or []
    metrics = metrics or []
    sse = sse or []
    S = {"agent": agent, "sid": sid, "rows": rows}
    # --- tool calls: PreToolUse deduplicated by tool_use_id, Post and Claude tool_result joined by id
    calls, by_tuid, dup_pre = [], {}, 0
    posts = []
    for r in rows:
        if r["ev"] == "agent.hook.PreToolUse":
            key = r.get("tuid") or f"pre@{r['ts']}"
            if key in by_tuid:
                dup_pre += 1
                continue
            c = {"agent": agent, "tuid": r.get("tuid"), "at": r["at"], "tool": r.get("tool") or "?", "tin": r.get("tin") or "",
                 "hash": (r.get("tool") or "") + ":" + str(r.get("tin_hash") or hashlib.sha1((r.get("tin") or "").encode()).hexdigest()),
                 "aid": r.get("aid") or "", "atype": r.get("atype") or "", "pm": r.get("pm") or "",
                 "mcp": mcp_server_of(r.get("tool") or "", r.get("mcp_server") or ""), "post_at": None,
                 "resp_head": "", "resp_tail": "", "resp_len": 0, "dur_s": None, "cc_success": None, "cc_err": None,
                 "turn_id": r.get("turn_id") or "", "prompt_id": r.get("prompt_id") or "", "row": r}
            by_tuid[key] = c
            calls.append(c)
        elif r["ev"] == "agent.hook.PostToolUse":
            posts.append(r)
    orphan_posts = 0
    for r in posts:
        c = by_tuid.get(r.get("tuid"))
        if c is None:
            orphan_posts += 1
            c = {"agent": agent, "tuid": r.get("tuid"), "at": r["at"], "tool": r.get("tool") or "?", "tin": r.get("tin") or "",
                 "hash": (r.get("tool") or "") + ":" + str(r.get("tin_hash") or ""), "aid": r.get("aid") or "",
                 "atype": r.get("atype") or "", "pm": r.get("pm") or "",
                 "mcp": mcp_server_of(r.get("tool") or "", r.get("mcp_server") or ""), "post_at": None,
                 "resp_head": "", "resp_tail": "", "resp_len": 0, "dur_s": None, "cc_success": None, "cc_err": None,
                 "turn_id": r.get("turn_id") or "", "prompt_id": r.get("prompt_id") or "", "row": r, "no_pre": True}
            by_tuid[r.get("tuid") or f"post@{r['ts']}"] = c
            calls.append(c)
        if c["post_at"] is not None:
            continue
        c["post_at"] = r["at"]
        c["resp_head"], c["resp_tail"], c["resp_len"] = r.get("resp_head") or "", r.get("resp_tail") or "", _int(r.get("resp_len")) or 0
        c["hx"], c["hstatus"] = _int(r.get("hx")), (r.get("hstatus") or "").strip().lower()
        c["resp_hash"] = r.get("resp_hash") or None
        d = _num(r.get("dur"))
        if d is None:
            d = _num(r.get("hdur"))
        c["dur_s"] = d / 1000.0 if d is not None else (r["at"] - c["at"]).total_seconds()
    for e in claude:
        if e["e"] == "tool_result" and e.get("tuid") in by_tuid:
            c = by_tuid[e["tuid"]]
            c["cc_success"] = str(e.get("success")).lower() == "true"
            c["cc_err"] = e.get("err") or None
            d = _num(e.get("dur"))
            if d is not None:
                c["dur_s"] = d / 1000.0
    calls.sort(key=lambda c: c["at"])
    for c in calls:
        c["state"], c["note"] = call_state(c)
        c["name"] = display_tool(c["tool"])
        c["is_wait"] = c["name"] in POLL_TOOLS or c["tool"] in POLL_TOOLS
        c["is_edit"] = c["tool"] in EDIT_TOOLS
        args = _json(c["tin"]) if c["tin"] else None
        c["args"] = args if isinstance(args, dict) else {}
        cmd = c["args"].get("command") if c["tool"] in ("Bash", "shell", "exec_command") else None
        c["cmd"] = cmd if isinstance(cmd, str) else None
    S["calls"], S["dup_pre"], S["orphan_posts"] = calls, dup_pre, orphan_posts

    # --- prompts, stops, lifecycle
    prompts = []
    for r in rows:
        if r["ev"] == "agent.hook.UserPromptSubmit":
            text, kind = T.prompt_text(r.get("prompt") or "")
            prompts.append({"at": r["at"], "text": text, "kind": kind, "turn_id": r.get("turn_id") or "",
                            "prompt_id": r.get("prompt_id") or "", "title": r.get("title") or "",
                            "raw": (r.get("prompt") or "")[:300]})
    stops = [r for r in rows if r["ev"] == "agent.hook.Stop"]
    sub_start = [r for r in rows if r["ev"] == "agent.hook.SubagentStart"]
    sub_stop = [r for r in rows if r["ev"] == "agent.hook.SubagentStop"]
    pre_c = [r for r in rows if r["ev"] == "agent.hook.PreCompact"]
    post_c = [r for r in rows if r["ev"] == "agent.hook.PostCompact"]
    interrupts = [r for r in rows if r["ev"] == "agent.hook.Interrupt"]
    starts = [r for r in rows if r["ev"] == "agent.hook.SessionStart"]
    # compaction: each PostCompact, plus PreCompact without a PostCompact within 10 min
    compacts = list(post_c)
    for r in pre_c:
        if not any(0 <= (p["at"] - r["at"]).total_seconds() <= 600 for p in post_c):
            compacts.append(r)
    compacts.sort(key=lambda r: r["at"])

    # --- Claude native OTel
    api = []
    for e in claude:
        if e["e"] == "api_request":
            inp, cread, ccreate = _int(e.get("inp")) or 0, _int(e.get("cread")) or 0, _int(e.get("ccreate")) or 0
            api.append({"at": e["at"], "model": e.get("model") or None, "input": inp + cread + ccreate, "cached": cread,
                        "created": ccreate, "output": _int(e.get("outp")) or 0, "reasoning": None,
                        "cost": _num(e.get("cost")), "prompt_id": e.get("prompt_id") or "", "src": "claude"})
    api_errors = [e for e in claude if e["e"] == "api_error"]
    decisions = [e for e in claude if e["e"] == "tool_decision"]
    # --- Codex tokens linked through conversation.id (codex.sse_event response.completed)
    for e in sse:
        if e.get("kind") != "response.completed" or _int(e.get("inp")) is None:
            continue
        api.append({"at": e["at"], "model": e.get("model") or None, "input": _int(e.get("inp")) or 0,
                    "cached": _int(e.get("cached")) or 0, "created": 0, "output": _int(e.get("outp")) or 0,
                    "reasoning": _int(e.get("reas")), "prompt_id": "", "src": "codex_sse",
                    "cost": T.usage_cost(_int(e.get("inp")) or 0, _int(e.get("cached")) or 0, _int(e.get("outp")) or 0,
                                         e.get("model"), None)})
    # --- Codex tokens per turn from Stop, taken by the hottell drainer from the session rollout.
    # Ход с итогом из журнала не досчитывается ещё и по codex.sse_event (см. ниже, после разметки ходов).
    for r in stops:
        n = _int(r.get("t_resp"))
        if not n or _int(r.get("t_in")) is None:
            continue
        inp, cached, outp = _int(r.get("t_in")) or 0, _int(r.get("t_cached")) or 0, _int(r.get("t_out")) or 0
        model = r.get("t_model") or r.get("model") or None
        api.append({"at": r["at"], "model": model, "input": inp, "cached": cached, "created": 0, "output": outp,
                    "reasoning": _int(r.get("t_reas")), "prompt_id": "", "src": "hook_transcript", "responses": n,
                    "turn_id": r.get("turn_id") or "", "cost": T.usage_cost(inp, cached, outp, model, None)})
    api.sort(key=lambda a: a["at"])
    S["api"], S["api_errors"], S["decisions"] = api, api_errors, decisions
    S["sse_seen"] = any(a["src"] == "codex_sse" for a in api)

    # --- turns: Codex main-thread turn_id, Claude prompt_id; other rows join the turn open at their time
    main_key = (lambda r: (r.get("turn_id") or "") if not r.get("aid") else "") if agent == "codex" else (lambda r: r.get("prompt_id") or "")
    turns, tmap = [], {}
    for r in rows:
        k = main_key(r)
        if k and k not in tmap:
            tmap[k] = {"key": k, "start": r["at"], "last": r["at"], "stop": None, "state": "open", "prompt": None,
                       "prompt_at": None, "answer": None, "tok": {"input": 0, "cached": 0, "output": 0}}
            turns.append(tmap[k])
    starts_at = [t["start"] for t in turns]

    def turn_at(at, key=""):
        if key and key in tmap:
            return tmap[key]
        i = bisect.bisect_right(starts_at, at) - 1
        return turns[i] if i >= 0 else None

    for r in rows:
        t = turn_at(r["at"], main_key(r))
        if t is not None and r["ev"] not in ("agent.hook.SessionStart", "agent.hook.SessionEnd"):
            t["last"] = max(t["last"], r["at"])
    # Два источника токенов Codex: итог хода из журнала (Stop) точнее и полнее, OTel — по ответам.
    # Ответы OTel, попавшие в ход с итогом из журнала, отбрасываются, чтобы не считать их дважды.
    whole = [turn_at(a["at"], a.get("turn_id") or "") for a in api if a["src"] == "hook_transcript"]
    whole = {id(t) for t in whole if t is not None}
    if whole:
        api = [a for a in api if not (a["src"] == "codex_sse" and (t := turn_at(a["at"])) is not None and id(t) in whole)]
        S["api"] = api
    for a in api:
        t = turn_at(a["at"], a.get("prompt_id"))
        if t is not None:
            t["last"] = max(t["last"], a["at"])
            for k in ("input", "cached", "output"):
                t["tok"][k] += a[k] or 0
    for r in stops:
        t = turn_at(r["at"], main_key(r))
        if t is not None and t["stop"] is None:
            t["stop"], t["state"], t["answer"] = r["at"], "task_complete", r.get("last_msg") or None
    for r in interrupts:
        t = turn_at(r["at"], main_key(r))
        if t is not None and t["stop"] is None:
            t["stop"], t["state"] = r["at"], "turn_aborted"
    for p in prompts:
        t = turn_at(p["at"], p["turn_id"] if agent == "codex" else p["prompt_id"])
        if t is not None and t["prompt"] is None:
            t["prompt"], t["prompt_at"] = p["text"], p["at"]
            t["start"] = min(t["start"], p["at"])
    for t in turns:
        t["end_eff"] = t["stop"] or t["last"]
        t["dur_ms"] = int((t["end_eff"] - t["start"]).total_seconds() * 1000)
    S["turns"], S["prompts"], S["stops"], S["compacts"], S["interrupts"] = turns, prompts, stops, compacts, interrupts
    S["sub_start"], S["sub_stop"], S["session_starts"] = sub_start, sub_stop, starts

    # --- waits: вопрос вам -> ответ. Обычный вопрос держит агента до своего результата (ответ часто приходит
    # результатом вызова, без новой реплики): конец — PostToolUse или ваша реплика, что раньше. Async-вопрос агента
    # не держит: конец — ваша следующая реплика. Простой начинается, когда закончились вызовы основного агента, уже
    # шедшие в момент вопроса (пока выполнялся другой вызов агента — не простой). Вызов без известного конца мешает
    # остановке, только если он из того же хода, что и вопрос: новый ход начинается с вашей реплики, и вызов прошлого
    # хода без PostToolUse — потерянный результат, а не работа. Остановка — простой не короче STOP_MIN_S, внутри
    # которого основной агент не начал ни одного вызова; без конца — если после вопроса основной агент ничего
    # не начинал. Интервалы простоя объединяются.
    # Не остановки: вопрос без PreToolUse (нет времени вопроса) и обычный вопрос, вернувший ошибку (он не задан:
    # например, Codex отказывает в request_user_input вне Plan mode).
    p_at = [p["at"] for p in prompts]
    main = [c for c in calls if not c["aid"] and c["tool"] not in WAIT_TOOLS]
    main_at = sorted(c["at"] for c in calls if not c["aid"])
    waits = []
    for c in calls:
        if c["tool"] not in WAIT_TOOLS or c.get("no_pre"):
            continue
        blocking = not c["tool"].endswith("_async")
        failed = blocking and c["state"] == "error"
        i = bisect.bisect_right(p_at, c["at"])
        reply = prompts[i] if i < len(prompts) else None
        end = reply["at"] if reply and not failed else None
        if blocking and not failed and c["post_at"] is not None:
            end = min(c["post_at"], end) if end else c["post_at"]
        turn = turn_at(c["at"], main_key(c["row"]))
        ends, unknown = [], False
        for x in main:
            if x["at"] > c["at"]:
                continue
            x_end = _call_end(x)
            if x_end is None:
                unknown = unknown or turn_at(x["at"], main_key(x["row"])) is turn
            elif x_end > c["at"]:
                ends.append(x_end)
        idle_from = max([c["at"]] + ends)
        j = bisect.bisect_right(main_at, c["at"])
        short = end is not None and (end - idle_from).total_seconds() < STOP_MIN_S
        if failed or short or unknown:
            stopped = False
        elif end is None:
            stopped = j >= len(main_at)
        else:
            stopped = not (j < len(main_at) and main_at[j] < end)
        waits.append({"call": c, "at": c["at"], "idle_from": idle_from, "reply": reply, "end": end, "stopped": stopped,
                      "failed": failed, "short": short, "min": T.minutes(idle_from, end) if end else None})
    for e in decisions:
        if (e.get("dsource") or "") == "user":                      # окно разрешения Claude блокирует ход
            waits.append({"call": None, "at": e["at"], "reply": None, "end": None, "min": None, "stopped": True,
                          "decision": e})
    S["waits"] = waits
    stopped = [w for w in waits if w["stopped"] and w.get("call") is not None]
    iv = sorted((w["idle_from"], w["end"]) for w in stopped if w["end"])
    total, ca, cb = 0.0, None, None
    for a, b in iv:
        if cb is None or a > cb:
            if cb is not None:
                total += T.minutes(ca, cb)
            ca, cb = a, b
        else:
            cb = max(cb, b)
    if cb is not None:
        total += T.minutes(ca, cb)
    S["wait_min"] = round(total, 1) if iv else (None if any(w["stopped"] for w in waits) else 0.0)

    # --- user time: answer / question / last event of an open turn -> your next prompt, ≤ 30 min
    anchors = sorted([t["stop"] for t in turns if t["stop"]] + [w["at"] for w in waits if w["stopped"]]
                     + [t["end_eff"] for t in turns if t["state"] == "open"])
    gaps, prev, away = [], None, 0
    for p in prompts:
        lo = bisect.bisect_right(anchors, prev) if prev is not None else 0
        hi = bisect.bisect_left(anchors, p["at"])
        if hi > lo:
            g = T.minutes(anchors[hi - 1], p["at"])
            gaps.append((p, g if g <= T.AWAY_MIN else 0.0))
            away += g > T.AWAY_MIN
        prev = p["at"]
    S["user_gaps"], S["away"] = gaps, away

    # --- Claude metrics
    msum = collections.defaultdict(float)
    active_pts = []
    for m in metrics:
        v = _num(m.get("value")) or 0.0
        msum[(m["metric"], m.get("type") or "")] += v
        if m["metric"] == "claude_code.active_time.total":
            active_pts.append((parse_ch_ts(m.get("ts")), m.get("type") or "", v))
    S["metrics"], S["active_pts"] = msum, active_pts

    # --- commits / PRs from hook payloads (Codex) — only with a success signal in the output
    commits = prs = 0
    stats, unconfirmed = [], 0
    for c in calls:
        if not c["cmd"]:
            continue
        segs = [T._strip_prefix(s) for s in T.split_segments(c["cmd"])]
        is_commit = any(s and os.path.basename(s[0]) == "git" and T._positional(s)[:1] == ["commit"] for s in segs)
        is_pr = any(s and os.path.basename(s[0]) == "gh" and T._positional(s)[:2] == ["pr", "create"] for s in segs)
        out = c["resp_head"] + "\n" + c["resp_tail"]
        if is_commit:
            if c["state"] != "error" and _GIT_COMMIT_OUT.search(out):
                commits += 1
                m = _STAT.search(out)
                stats.append((int(m.group(2) or 0), int(m.group(3) or 0)) if m else None)
            else:
                unconfirmed += 1
        if is_pr:
            if c["state"] != "error" and _PR_URL.search(out):
                prs += 1
            else:
                unconfirmed += 1
    S["commits"], S["prs"], S["commit_stats"], S["commit_unconfirmed"] = commits, prs, stats, unconfirmed

    # --- skills
    snapshot = None
    for r in starts:
        snap = _json(r.get("snapshot") or "")
        if isinstance(snap, dict) and isinstance(snap.get("items"), list):
            snapshot = snap
    S["snapshot"] = snapshot
    cwd = collections.Counter(r.get("cwd") for r in rows if r.get("cwd")).most_common(1)
    S["cwd"] = cwd[0][0] if cwd else None
    acts = []
    for c in calls:
        if c["tool"] == "Skill":
            acts.append({"call": c, "name": str(c["args"].get("skill") or c["args"].get("name") or "?"), "path": None,
                         "source": "Claude Skill"})
            continue
        paths = []
        if c["tool"] == "Read" and str(c["args"].get("file_path") or "").endswith("SKILL.md"):
            paths = [c["args"]["file_path"]]
        elif c["cmd"]:
            paths = T.skill_md_reads(c["cmd"], None, S["cwd"])
        for p in paths:
            acts.append({"call": c, "name": skill_name(p), "path": p, "source": T.skill_source(p)})
    S["skill_acts"] = acts
    S["models"] = collections.Counter(r.get("model") for r in rows if r.get("model"))
    S["title"] = next((p["title"] for p in reversed(prompts) if p["title"]), None)
    S["created"] = uuid7_time(sid)
    S["start"] = min([r["at"] for r in rows] + [a["at"] for a in api] + [e["at"] for e in claude]) if rows else None
    S["end"] = max([r["at"] for r in rows] + [a["at"] for a in api] + [e["at"] for e in claude]) if rows else None
    first_hook = rows[0]["at"] if rows else None
    S["partial"] = bool(S["created"] and first_hook and (first_hook - S["created"]).total_seconds() > PARTIAL_AFTER_H * 3600) \
        or resumed_without_start(starts)
    S["first_hook"] = first_hook
    S["kind"], S["kind_reason"] = classify_session(S)
    _events(S)
    _friction(S)
    return S


def _call_end(c):
    """Когда вызов закончился: PostToolUse, а у Claude без него — начало + длительность из tool_result; None — неизвестно."""
    if c["post_at"] is not None:
        return c["post_at"]
    return c["at"] + dt.timedelta(seconds=c["dur_s"]) if c["dur_s"] is not None else None


def resumed_without_start(starts):
    """True, если запись началась с возврата к сессии: первый SessionStart с source startup|clear|resume — resume.
    compact (Claude Code) — середина сессии, границу записи не показывает и пропускается."""
    for r in sorted(starts, key=lambda r: r["at"]):
        src = r.get("source") or ""
        if src in ("startup", "clear"):     # clear в Claude Code начинает новую сессию
            return False
        if src == "resume":
            return True
    return False


def classify_session(S):
    """(kind, reason): system — явная служебная задача Codex; automation — запуск по расписанию; user — остальное."""
    first = next((p for p in S["prompts"] if p["kind"] == "prompt"), None)
    text = " ".join((first["raw"] if first else "").split())
    for rx, why in SYSTEM_PROMPTS:
        if rx.match(text):
            return "system", why
    if S["cwd"] and SYSTEM_CWD.search(S["cwd"]):
        return "system", "папка ~/.codex/memories"
    if AUTOMATION_PROMPT.match(text):
        return "automation", "реплика <heartbeat>"
    return "user", None


def _events(S):
    """Timeline events in time order; line = 1..N. Keeps the line of every object for evidence."""
    raw = []
    wait_by_call = {id(w["call"]): w for w in S["waits"] if w.get("call")}
    for p in S["prompts"]:
        raw.append((p["at"], 0, "prompt", p))
    for c in S["calls"]:
        raw.append((c["at"], 1, "call", c))
    for a in S["api"]:
        raw.append((a["at"], 2, "api", a))
    for e in S["api_errors"]:
        raw.append((e["at"], 2, "api_error", e))
    for r in S["stops"]:
        raw.append((r["at"], 3, "stop", r))
    for r in S["compacts"]:
        raw.append((r["at"], 1, "compact", r))
    for r in S["sub_start"]:
        raw.append((r["at"], 1, "sub_start", r))
    for r in S["sub_stop"]:
        raw.append((r["at"], 1, "sub_stop", r))
    for r in S["interrupts"]:
        raw.append((r["at"], 3, "abort", r))
    for s in S["skill_acts"]:
        raw.append((s["call"]["at"], 1.5, "skill", s))
    raw.sort(key=lambda x: (x[0], x[1]))
    turn_starts = [t["start"] for t in S["turns"]]

    def tnum(at):
        i = bisect.bisect_right(turn_starts, at) - 1
        return max(i, 0)

    events = []
    for at, _, kind, obj in raw:
        line = len(events) + 1
        obj["_line"] = line
        ev = {"line": line, "at": T.iso(at), "turn": tnum(at), "k": None, "x": None, "note": None, "side": None, "tool": None}
        if kind == "prompt":
            ev.update(k="prompt", x=T.clean(obj["text"], 240) or "—", side="вы",
                      note="ответ на вопрос агента" if obj["kind"] == "reply" else None)
        elif kind == "call":
            c = obj
            ev["tool"] = c["name"]
            ev["x"] = tool_summary(c["tool"], c["tin"])
            ev["side"] = T.fmt_dur(c["dur_s"]) if c["dur_s"] is not None else None
            if c["tool"] in WAIT_TOOLS:
                w = wait_by_call.get(id(c))
                ev["k"] = "wait"
                ev["side"] = ("вопрос без PreToolUse" if w is None else
                              "вопрос не прошёл" if w["failed"] else
                              "ответ сразу" if w["short"] and w["idle_from"] == w["at"] else
                              "агент продолжал работу" if not w["stopped"] else
                              f"ждал {w['min']:.1f} мин" if w["min"] is not None else "без ответа")
            elif c["tool"] in ("Agent", "SendMessage") or c["name"].startswith("collaboration."):
                ev["k"] = "agent"
            else:
                ev["k"] = "err" if c["state"] == "error" else "tool"
            if c["state"] in ("error", "unknown"):
                ev["note"] = c["note"]
            if c["aid"]:
                ev["note"] = T.clean(f"субагент {c['atype'] or c['aid'][:8]}" + (f" · {ev['note']}" if ev["note"] else ""), 160)
        elif kind == "api":
            ratio = (obj["cached"] / obj["input"] * 100) if obj["input"] else 0
            ev.update(k="api", x=f"{obj['model'] or 'модель?'} · вход {T.fmt_ktok(obj['input'])} (кэш {ratio:.0f}%)",
                      note=f"выход {obj['output']}" + (" · codex.sse_event" if obj["src"] == "codex_sse" else
                                                         f" · {obj.get('responses')} отв. за ход · из журнала сессии" if obj["src"] == "hook_transcript" else ""),
                      side=T.fmt_usd(obj["cost"]) if obj["cost"] is not None else None)
        elif kind == "api_error":
            ev.update(k="err", tool="api", x=T.clean(f"ошибка API · {obj.get('status') or ''} {obj.get('err') or ''}", 140))
        elif kind == "stop":
            ev.update(k="answer", x=T.clean(obj.get("last_msg"), 240) or "—")
        elif kind == "compact":
            ev.update(k="compact", x=f"сжатие контекста ({obj.get('trig') or 'триггер не указан'})")
        elif kind == "sub_start":
            ev.update(k="agent", x=T.clean(f"субагент запущен · {obj.get('atype') or obj.get('aid') or ''}", 140))
        elif kind == "sub_stop":
            ev.update(k="agent", x=T.clean(f"субагент завершён · {obj.get('atype') or obj.get('aid') or ''}", 140),
                      note=T.clean(obj.get("last_msg"), 160))
        elif kind == "abort":
            ev.update(k="abort", x=T.clean(f"ход прерван · {obj.get('reason') or 'Interrupt'}", 140))
        elif kind == "skill":
            ev.update(k="skill", x=T.clean(f"skill · {obj['name']}", 140), tool=obj["call"]["name"],
                      note=T.clean(obj["path"], 160) if obj["path"] else None)
        events.append(ev)
    S["events"] = events


def _friction(S):
    sid = S["sid"]
    out = {k: [] for k in T.FRICTION_KEYS}

    def ev(obj, text, cost=None):
        return {"sid": sid, "line": obj.get("_line"), "at": T.iso(obj.get("at")), "text": T.clean(text, 200), "_cost": cost}

    calls = S["calls"]
    used = set()
    for i, c in enumerate(calls):
        if id(c) in used or c["is_wait"] or c["state"] != "error":
            continue
        same = [x for x in calls[i:i + T.RETRY_WINDOW] if x["hash"] == c["hash"]]
        if len(same) < T.RETRY_MIN or any(x["state"] != "error" for x in same):
            continue
        lo, hi = same[0]["at"], same[-1]["at"]
        if any(lo < x["at"] < hi and x["is_edit"] for x in calls):
            continue
        used.update(id(x) for x in same)
        out["retry"].append({"evidence": [ev(x, f"повтор {k + 1}/{len(same)} · {tool_summary(x['tool'], x['tin'], 80)} · "
                                                f"{x['note'] or 'ошибка'}") for k, x in enumerate(same)]})
    streak = []
    for c in calls:
        if c["cmd"] and T.is_test_cmd(c["cmd"]) and c["state"] in ("error", "success"):
            if c["state"] == "success":
                if len(streak) >= T.THRASH_MIN:
                    out["thrash"].append({"evidence": [ev(x, f"провал {k + 1} · {T.normalize_cmd(x['cmd'])}") for k, x in enumerate(streak)]})
                streak = []
                continue
            if streak and not any(streak[-1]["at"] < x["at"] < c["at"] and x["is_edit"] for x in calls):
                if len(streak) >= T.THRASH_MIN:
                    out["thrash"].append({"evidence": [ev(x, f"провал {k + 1} · {T.normalize_cmd(x['cmd'])}") for k, x in enumerate(streak)]})
                streak = []
            streak.append(c)
    if len(streak) >= T.THRASH_MIN:
        out["thrash"].append({"evidence": [ev(x, f"провал {k + 1} · {T.normalize_cmd(x['cmd'])}") for k, x in enumerate(streak)]})
    by_server = collections.defaultdict(list)
    for c in calls:
        if c["mcp"] and c["state"] == "error":
            by_server[c["mcp"]].append(c)
    for server, items in by_server.items():
        if len(items) >= T.MCPFAIL_MIN:
            out["mcpfail"].append({"evidence": [ev(x, f"{x['name']} · {x['note'] or 'ошибка'}") for x in items[-8:]]})
    api = [a for a in S["api"] if a["src"] == "claude"]
    a_at = [a["at"] for a in api]
    for p in S["prompts"]:
        i = bisect.bisect_left(a_at, p["at"])
        if i == 0 or i >= len(api):
            continue
        pause = T.minutes(api[i - 1]["at"], p["at"])
        first = api[i]
        if pause <= T.COLD_PAUSE_MIN or not first["input"]:
            continue
        ratio = first["cached"] / first["input"]
        if ratio < T.COLD_RATIO:
            out["coldcache"].append({"evidence": [ev(first, f"пауза {pause:.0f} мин → кэш {ratio * 100:.0f}% из "
                                                           f"{T.fmt_ktok(first['input'])} входа", first["cost"])],
                                     "cost": first["cost"]})
    for r in S["compacts"]:
        out["compact"].append({"evidence": [ev(r, f"сжатие контекста ({r.get('trig') or 'триггер не указан'})")]})
    comp_at = [r["at"] for r in S["compacts"]]
    edits = collections.defaultdict(list)
    for c in calls:
        for p in edited_paths(c, S["cwd"]):
            edits[p].append(c["at"])
    reads = collections.defaultdict(list)
    for c in calls:
        targets = set(read_call_targets(c, S["cwd"]))
        sig = read_signature(c) if len(targets) == 1 else None      # ответ на несколько файлов не разделить
        if sig is None or read_failed(c):
            continue
        (path, span), = targets
        if not span.startswith("последние"):                         # хвост лога дописывает другой процесс
            reads[(c["aid"], path, span)].append((c, sig))
    for (aid, path, span), items in sorted(reads.items(), key=lambda kv: -len(kv[1])):
        name = os.path.basename(path) + ("" if span == "весь файл" else f" {span}") + (" · субагент" if aid else "")
        for run in reread_runs(items, sorted(comp_at + edits.get(path, []))):
            extra = run[1:]
            out["reread"].append({"path": path, "extra": len(extra), "bytes": sum(x["resp_len"] for x in extra),
                                  "evidence": [ev(x, f"{name} · чтение {k + 1}/{len(run)}") for k, x in enumerate(run)][-8:]})
    for w in S["waits"]:
        if w.get("call") is not None and w["stopped"]:
            tail = f"агент стоял {w['min']:.1f} мин до ответа" if w["min"] is not None else "агент остановился, ответа не было"
            out["wait"].append({"evidence": [ev(w["call"], f"{tail} · {tool_summary(w['call']['tool'], w['call']['tin'], 120)}")]})
    for r in S["interrupts"]:
        out["abort"].append({"evidence": [ev(r, f"ход прерван · {r.get('reason') or 'Interrupt'}")]})
    S["friction"] = out


# --------------------------------------------------------------------------- contract output


def session_fields(S):
    agent = S["agent"]
    calls = S["calls"]
    api = S["api"]
    claude_api = [a for a in api if a["src"] == "claude"]
    sse_api = [a for a in api if a["src"] == "codex_sse"]
    tr_api = [a for a in api if a["src"] == "hook_transcript"]
    tok = cost = reqs = cache_hit = None
    if api:
        tok = {"input": sum(a["input"] for a in api), "cached": sum(a["cached"] for a in api),
               "output": sum(a["output"] for a in api),
               "reasoning": (sum(a["reasoning"] or 0 for a in api) if any(a["reasoning"] is not None for a in api) else None)}
        reqs = sum(a.get("responses") or 1 for a in api)
        cache_hit = round(tok["cached"] / tok["input"], 3) if tok["input"] else None
        costs = [a["cost"] for a in api]
        cost = round(sum(costs), 4) if all(c is not None for c in costs) else None
    model = None
    if claude_api:
        model = collections.Counter(a["model"] for a in claude_api if a["model"]).most_common(1)
        model = model[0][0] if model else None
    if model is None and S["models"]:
        model = S["models"].most_common(1)[0][0]
    active_cli = S["metrics"].get(("claude_code.active_time.total", "cli"))
    active_user = S["metrics"].get(("claude_code.active_time.total", "user"))
    active_min = round(active_cli / 60.0, 1) if active_cli is not None else round(sum(t["dur_ms"] for t in S["turns"]) / 60000.0, 1)
    user_min = round(active_user / 60.0, 1) if active_user is not None else round(float(sum(g for _, g in S["user_gaps"])), 1)
    first = next((p for p in S["prompts"] if p["kind"] == "prompt"), None)
    commits, prs, added, removed = S["commits"], S["prs"], None, None
    if agent == "claude" and S["metrics"]:
        commits = int(S["metrics"].get(("claude_code.commit.count", ""), 0))
        prs = int(S["metrics"].get(("claude_code.pull_request.count", ""), 0))
        if ("claude_code.lines_of_code.count", "added") in S["metrics"] or ("claude_code.lines_of_code.count", "removed") in S["metrics"]:
            added = int(S["metrics"].get(("claude_code.lines_of_code.count", "added"), 0))
            removed = int(S["metrics"].get(("claude_code.lines_of_code.count", "removed"), 0))
    elif S["commit_stats"] and all(x is not None for x in S["commit_stats"]):
        added, removed = sum(x[0] for x in S["commit_stats"]), sum(x[1] for x in S["commit_stats"])
    elif not S["commit_stats"] and not S["partial"]:
        added, removed = (0, 0) if agent == "codex" and not S["commit_unconfirmed"] else (None, None)
    if agent == "claude":
        otel = "recorded" if (claude_api or S["metrics"]) else "missing"
    else:
        otel = "linked" if (sse_api or S.get("sse_seen")) else "unlinked"
    cost_basis = None
    if cost is not None:
        cost_basis = "otel_reported" if claude_api else "api_price_estimate"
    hook_counts = collections.Counter(r["ev"].replace("agent.hook.", "") for r in S["rows"])
    flags = [k for k in T.FRICTION_KEYS if S["friction"].get(k)]
    return {
        "id": S["sid"], "short": T.short_id(S["sid"]), "agent": agent,
        "kind": S["kind"], "kind_reason": S["kind_reason"],
        "source_path": None, "source_sha256": None, "records": len(S["rows"]),
        "project": T.project_name(S["cwd"]), "cwd": S["cwd"], "branch": None,
        "start": T.iso(S["start"]), "end": T.iso(S["end"]),
        "wall_min": round(T.minutes(S["start"], S["end"]), 1) if S["start"] else None,
        "active_min": active_min, "user_min": user_min,
        "turns": len(S["turns"]) or len(S["stops"]), "prompts": len(S["prompts"]), "corrections": None,
        "reqs": reqs, "model": model, "tok": tok, "cache_hit": cache_hit, "cost_usd": cost, "cost_basis": cost_basis,
        "calls": len(calls), "errors": sum(c["state"] == "error" for c in calls),
        "unknown_results": sum(c["state"] == "unknown" for c in calls),
        "outcome_known": sum(c["state"] in ("success", "error") for c in calls),
        "commits": commits, "prs": prs, "added": added, "removed": removed,
        "first": T.clean(first["text"], 200) if first else None,
        "title": T.clean(S["title"], 200) if S["title"] else (T.clean(first["text"], 200) if first else None),
        "flags": flags, "compactions": len(S["compacts"]), "aborted": len(S["interrupts"]),
        "waits": len(S["waits"]), "wait_min": S["wait_min"],
        "outcome": "unknown", "outcome_basis": None, "tasks": [], "checks": [], "deep": None,
        "sources": {"transcript": "not_used", "hooks": "partial" if S["partial"] else "recorded", "otel": otel,
                    "deep": "none", "coverage": False, "test_review": False,
                    "transcript_facts": "recorded" if (tr_api or any(c.get("hx") is not None or c.get("hstatus") for c in calls)) else "missing",
                    "hook_events": dict(sorted(hook_counts.items()))},
        "daily": session_daily(S),
    }


def session_daily(S):
    days = {}

    def d(day):
        if day not in days:
            days[day] = {"date": day, "cost_usd": None, "reqs": 0, "tok": None, "agent_min": 0.0, "user_min": 0.0,
                         "calls": 0, "errors": 0}
        return days[day]

    for a in S["api"]:
        x = d(T._day(a["at"]))
        x["reqs"] += 1
        if x["tok"] is None:
            x["tok"] = {"input": 0, "cached": 0, "output": 0, "reasoning": None}
        for k in ("input", "cached", "output"):
            x["tok"][k] += a[k] or 0
        if a["cost"] is not None:
            x["cost_usd"] = (x["cost_usd"] or 0.0) + a["cost"]
    cli_pts = [(at, v) for at, typ, v in S["active_pts"] if typ == "cli" and at is not None]
    if cli_pts:
        for at, v in cli_pts:
            d(T._day(at))["agent_min"] += v / 60.0
    else:
        for t in S["turns"]:
            for day, m in T._split_by_day(t["start"], t["end_eff"]):
                d(day)["agent_min"] += m
    for p, g in S["user_gaps"]:
        d(T._day(p["at"]))["user_min"] += g
    for c in S["calls"]:
        x = d(T._day(c["at"]))
        x["calls"] += 1
        x["errors"] += c["state"] == "error"
    out = []
    for day in sorted(days):
        x = days[day]
        if not S["api"]:
            x["reqs"] = None
        x["cost_usd"] = round(x["cost_usd"], 4) if x["cost_usd"] is not None else None
        x["agent_min"], x["user_min"] = round(x["agent_min"], 1), round(x["user_min"], 1)
        out.append(x)
    return out


def session_timeline(S):
    start = S["start"]
    cum, ctx, total = [], [], 0.0
    for a in S["api"]:
        mins = round(T.minutes(start, a["at"]), 2)
        if a["cost"] is not None:
            if not cum:
                cum.append([0, 0])
            total += a["cost"]
            cum.append([mins, round(total, 4)])
        ctx.append([mins, round(a["input"] / 1000, 1)])
    tools = collections.Counter(c["name"] for c in S["calls"])
    turns = []
    for i, t in enumerate(S["turns"]):
        prompt_line = next((p["_line"] for p in S["prompts"] if p["at"] == t["prompt_at"]), None) if t["prompt_at"] else None
        turns.append({"turn": i, "turn_id": t["key"], "start": T.iso(t["start"]), "end": T.iso(t["stop"]),
                      "dur_ms": t["dur_ms"], "state": t["state"], "prompt_line": prompt_line,
                      "prompt": T.clean(t["prompt"], 200), "answer": T.clean(t["answer"], 300),
                      "tok": t["tok"] if S["api"] else None})
    return {"id": S["sid"], "events": S["events"], "cum": cum, "ctx": ctx, "tools": dict(tools.most_common()), "turns": turns}


def aggregate_tools(sessions):
    rows = {}
    for S in sessions:
        for c in S["calls"]:
            key = c["name"]
            r = rows.setdefault(key, {"name": key, "kind": "mcp" if c["mcp"] else "builtin", "server": c["mcp"], "calls": 0,
                                      "errors": 0, "unknown": 0, "p50_s": None, "p95_s": None, "by_session": {}, "_d": []})
            err, unk = c["state"] == "error", c["state"] == "unknown"
            r["calls"] += 1
            r["errors"] += err
            r["unknown"] += unk
            r["_d"].append(c["dur_s"])
            bs = r["by_session"].setdefault(S["sid"], {"calls": 0, "errors": 0, "unknown": 0})
            bs["calls"] += 1
            bs["errors"] += err
            bs["unknown"] += unk
    out = []
    for r in rows.values():
        d = r.pop("_d")
        r["p50_s"], r["p95_s"] = T.pct(d, 0.5), T.pct(d, 0.95)
        out.append(r)
    return sorted(out, key=lambda r: -r["calls"])


def aggregate_mcp(sessions):
    rows = {}
    for S in sessions:
        for c in S["calls"]:
            if not c["mcp"]:
                continue
            r = rows.setdefault(c["mcp"], {"server": c["mcp"], "calls": 0, "errors": 0, "unknown": 0, "avg_s": None,
                                           "by_session": {}, "_d": []})
            err, unk = c["state"] == "error", c["state"] == "unknown"
            r["calls"] += 1
            r["errors"] += err
            r["unknown"] += unk
            r["_d"].append(c["dur_s"])
            bs = r["by_session"].setdefault(S["sid"], {"calls": 0, "errors": 0, "unknown": 0})
            bs["calls"] += 1
            bs["errors"] += err
            bs["unknown"] += unk
    out = []
    for r in rows.values():
        d = [x for x in r.pop("_d") if x is not None]
        r["avg_s"] = round(sum(d) / len(d), 2) if d else None
        out.append(r)
    return sorted(out, key=lambda r: -r["calls"])


def aggregate_commands(sessions, fields):
    proj = {f["id"]: f["project"] for f in fields}
    rows = {}
    for S in sessions:
        for c in S["calls"]:
            if not c["cmd"]:
                continue
            key = (T.normalize_cmd(c["cmd"]), proj.get(S["sid"]))
            r = rows.setdefault(key, {"cmd": key[0], "project": key[1], "runs": 0, "failed": 0, "unknown": 0, "p95_s": None,
                                      "total_min": 0.0, "by_session": {}, "_d": []})
            failed, unk = c["state"] == "error", c["state"] in ("unknown", "done")
            r["runs"] += 1
            r["failed"] += failed
            r["unknown"] += unk
            r["_d"].append(c["dur_s"])
            bs = r["by_session"].setdefault(S["sid"], {"runs": 0, "failed": 0, "unknown": 0})
            bs["runs"] += 1
            bs["failed"] += failed
            bs["unknown"] += unk
    out = []
    for r in rows.values():
        d = r.pop("_d")
        r["p95_s"] = T.pct(d, 0.95)
        r["total_min"] = round(sum(x for x in d if x is not None) / 60.0, 2)
        out.append(r)
    return sorted(out, key=lambda r: (-r["runs"], r["cmd"]))


_DECISION = {"accept": "разрешено", "reject": "отклонено"}
_DSOURCE = {"config": "настройками", "user": "вами", "hook": "хуком"}


def aggregate_permissions(sessions):
    rows = collections.OrderedDict()

    def bump(label, sid, n=1):
        r = rows.setdefault(label, {"label": label, "count": 0, "by_session": {}})
        r["count"] += n
        r["by_session"][sid] = r["by_session"].get(sid, 0) + n

    for S in sessions:
        if S["agent"] == "claude":
            for e in S["decisions"]:
                bump(f"Claude · {_DECISION.get(e.get('decision'), e.get('decision') or '?')} "
                     f"{_DSOURCE.get(e.get('dsource'), e.get('dsource') or '')}".strip(), S["sid"])
        else:
            for c in S["calls"]:
                bump(f"Codex · режим {c['pm'] or 'не указан'}", S["sid"])
        n = sum(1 for w in S["waits"] if w.get("call") is not None and not w.get("failed"))
        if n:
            bump("агент спросил вас", S["sid"], n)
    return list(rows.values())


LIVE_HOW = {
    "compact": "События хуков PreCompact/PostCompact (сжатие посреди работы).",
    "coldcache": "Только Claude Code: ваша реплика после паузы > 5 мин от прошлого api_request; первый запрос после неё "
                 "читает из кэша < 50 % входа. Стоимость — cost_usd этих запросов по данным Claude Code.",
    "reread": "Одно место файла прочитано ≥ 3 раз подряд одним агентом (субагенты — отдельно) с одинаковым ответом "
              "(хэш записанного ответа), без сжатия контекста и правки файла между чтениями. Чтения без результата или с ошибкой, "
              "команды на несколько файлов и хвост логов (tail) не считаются.",
    "wait": "Вопрос вам (request_user_input*, AskUserQuestion), пока основной агент не сделал ни одного вызова. "
            "Обычный вопрос длится до своего результата (или до вашей реплики, если она раньше), async — до вашей "
            "следующей реплики. Async-вопросы, пока агент продолжает работу, не считаются; пока выполнялся другой вызов "
            "агента — не простой.",
    "abort": "Хук Interrupt (Codex); у Claude Code прерывание хуками не записывается.",
    "correction": "Разметки реплик в живом варианте нет — не считается.",
    "retry": "Один и тот же вызов (одинаковый tool_input) ≥ 3 раз в окне 10 вызовов, все с ошибкой, без правок между. "
             "Ошибки Bash у Codex не видны (нет кода выхода) — для них повторы не ловятся.",
    "thrash": "≥ 3 провала тестовой команды подряд с правками между ними (только где ошибка видна: Claude, apply_patch).",
}


def aggregate_friction(sessions):
    out = []
    for key, name, sev, how in T.FRICTION_META:
        occ = [(S["sid"], it) for S in sessions for it in S["friction"].get(key, [])]
        costs = [it.get("cost") for _, it in occ if it.get("cost") is not None]
        evidence = [_plain(e) for _, it in occ for e in it["evidence"]]
        out.append({"key": key, "name": name, "how": LIVE_HOW.get(key, how), "sev": sev,
                    "sessions": sorted({sid for sid, _ in occ}),
                    "by_session": dict(collections.Counter(sid for sid, _ in occ)),
                    "count": None if key == "correction" else len(occ),
                    "cost_usd": round(sum(costs), 4) if (key == "coldcache" and occ and len(costs) == len(occ)) else None,
                    "evidence": T.newest(evidence, 60)})
    return out


def aggregate_skills(sessions):
    available, complete = {}, True
    for S in sessions:
        snap = S["snapshot"]
        if snap:
            complete = complete and bool(snap.get("complete"))
            for it in snap["items"]:
                if isinstance(it, dict) and it.get("name"):
                    available[it["name"]] = it.get("path_class") or ""
    rows = collections.OrderedDict()
    for S in sessions:
        for a in S["skill_acts"]:
            r = rows.setdefault(a["name"], {"name": a["name"], "source": a["source"], "activations": 0, "sessions": [],
                                            "by_session": {}, "first_line": None, "to_code_min": None,
                                            "corrections_after": 0, "subagents": 0, "size_ktok": None, "state": "used",
                                            "evidence": []})
            r["activations"] += 1
            r["by_session"][S["sid"]] = r["by_session"].get(S["sid"], 0) + 1
            if S["sid"] not in r["sessions"]:
                r["sessions"].append(S["sid"])
            if r["first_line"] is None:
                r["first_line"] = a["call"].get("_line")
            if a["path"]:
                try:
                    r["size_ktok"] = round(os.path.getsize(os.path.expanduser(a["path"])) / 4 / 1000, 1)
                except OSError:
                    pass
            r["evidence"].append({"sid": S["sid"], "line": a["call"].get("_line"), "at": T.iso(a["call"]["at"]),
                                  "text": tool_summary(a["call"]["tool"], a["call"]["tin"], 140)})
    out = list(rows.values())
    unused = [n for n in available if n not in rows]
    if unused:
        out.append({"name": "…", "source": f"остальные {len(unused)} skills — 0 активаций", "activations": 0, "sessions": [],
                    "by_session": {}, "first_line": None, "to_code_min": None, "corrections_after": 0, "subagents": 0, "size_ktok": None,
                    "state": "unused", "evidence": []})
    names = set(available) | set(rows)
    return {"available": len(names) if available else None, "snapshot_complete": complete if available else None,
            "rows": out, "gantt": _gantt(sessions)}


def _gantt(sessions):
    def score(S):
        return len(S["skill_acts"]) + len(S["sub_start"]) + len(S["sub_stop"]) + sum(
            1 for c in S["calls"] if c["tool"] in ("Agent",) or c["name"].startswith("collaboration.spawn"))
    ranked = sorted(sessions, key=score, reverse=True)
    if not ranked or score(ranked[0]) == 0:
        return None
    S = ranked[0]
    act_times = [a["call"]["at"] for a in S["skill_acts"]] + [r["at"] for r in S["sub_start"] + S["sub_stop"]] + [
        c["at"] for c in S["calls"] if c["tool"] == "Agent" or c["name"].startswith("collaboration.")]
    busy = [t for t in S["turns"] if any(t["start"] <= x <= t["end_eff"] for x in act_times)]
    if not busy:
        return None
    lo, hi = min(t["start"] for t in busy), max(t["end_eff"] for t in busy)

    def inside(x):
        return x is not None and lo <= x <= hi

    turn_starts = [t["start"] for t in S["turns"]]

    def turn_end(at):
        i = bisect.bisect_right(turn_starts, at) - 1
        return S["turns"][i]["end_eff"] if i >= 0 else at

    rows = []
    segs_all = []
    by_skill = collections.OrderedDict()
    for a in S["skill_acts"]:
        at = a["call"]["at"]
        if inside(at):
            b = min(turn_end(at), hi)
            by_skill.setdefault(a["name"], []).append({"from": T.iso(at), "to": T.iso(b), "c": "skill", "label": T.clean(a["name"], 60)})
            segs_all.append((at, b))
    for name, segs in by_skill.items():
        rows.append({"name": T.clean(name, 60), "segs": segs})
    sub = []
    stops = sorted(S["sub_stop"], key=lambda r: r["at"])
    for r in S["sub_start"]:
        if inside(r["at"]):
            end = next((x["at"] for x in stops if x["at"] >= r["at"] and (x.get("aid") == r.get("aid") or not r.get("aid"))), None)
            sub.append({"from": T.iso(r["at"]), "to": T.iso(min(end or turn_end(r["at"]), hi)), "c": "subagent",
                        "label": T.clean(r.get("atype") or "субагент", 60)})
    for c in S["calls"]:
        if c["tool"] == "Agent" and inside(c["at"]):
            end = c["post_at"] or turn_end(c["at"])
            sub.append({"from": T.iso(c["at"]), "to": T.iso(min(end, hi)), "c": "subagent",
                        "label": T.clean(tool_summary("Agent", c["tin"], 60), 60)})
    if sub:
        rows.append({"name": "субагенты", "segs": sub})
    mitems = [(c["at"], c["post_at"] or c["at"], c["mcp"]) for c in S["calls"] if c["mcp"] and inside(c["at"])]
    if mitems:
        rows.append({"name": "MCP", "segs": [{"from": T.iso(a), "to": T.iso(b), "c": "mcp",
                                                 "label": f"{len(ls)} выз. · " + ", ".join(sorted(set(map(str, ls))))}
                                                for a, b, ls in T._merge_segments(mitems, T.MCP_MERGE_S)]})
    noskill = []
    for t in S["turns"]:
        a, b = max(t["start"], lo), min(t["end_eff"], hi)
        if b <= a:
            continue
        x = a
        for sa, sb in sorted(segs_all):
            if sb <= x or sa >= b:
                continue
            if sa > x:
                noskill.append((x, sa))
            x = max(x, sb)
        if x < b:
            noskill.append((x, b))
    if noskill:
        rows.append({"name": "без skills", "segs": [{"from": T.iso(a), "to": T.iso(b), "c": "noskill", "label": "ход без skill"}
                                                   for a, b in noskill if (b - a).total_seconds() >= 1]})
    marks = [{"at": T.iso(p["at"]), "text": "«" + (T.clean(p["text"], 28) or "") + "»", "line": p["_line"]}
             for p in S["prompts"] if inside(p["at"])]
    return {"sid": S["sid"], "title": f"{T.short_id(S['sid'])} · {S['agent']} · {T.project_name(S['cwd']) or '?'}",
            "from": T.iso(lo), "to": T.iso(hi), "rows": rows, "marks": marks}


# --------------------------------------------------------------------------- findings


def _finding(fid, sev, title, what, ev, sessions, *, impact=None, impact_by_session=None, impact_unit=None, where=None,
             snip=None, readiness="hypothesis", alternative_causes=(), preconditions=(), verification=None, pattern_id=None,
             kind="diagnostic", scope="work"):
    return {"id": "live:" + hashlib.sha1(fid.encode()).hexdigest()[:20], "sev": sev, "title": title, "what": what,
            "ev": ev, "impact": impact, "impact_by_session": impact_by_session, "impact_unit": impact_unit,
            "where": where, "snip": snip, "kind": kind, "pattern_id": pattern_id, "scope": scope,
            "readiness": readiness, "decision": "not_requested", "execution": "not_applied", "effect": "not_measured",
            "source": "detector", "lang": "ru", "sessions": sessions, "cause": None,
            "alternative_causes": list(alternative_causes), "preconditions": list(preconditions), "exceptions": [],
            "verification": verification, "rollback": None, "expected_effect": None}


RESULT_GRACE_S = 120   # вызов моложе 2 минут на момент сборки: результат ещё может прийти
LIVE_SESSION_S = 3600  # последний ход открыт, сессия молчит меньше часа: вызов может ещё выполняться


def collection_findings(sessions, codex_otel, now):
    out = []
    # (a) Codex: вызовы без PostToolUse — только в сессиях, начатых при работающих хуках
    codex = [S for S in sessions if S["agent"] == "codex" and S["calls"]]
    under = [S for S in codex if _started_under_hooks(S)]
    lost, total = [], 0
    for S in under:
        end = S["calls"][-1]["at"]
        last = S["turns"][-1] if S["turns"] else None
        running = last is not None and last["state"] == "open" and (now - S["end"]).total_seconds() < LIVE_SESSION_S
        for c in S["calls"]:
            if c.get("no_pre") or (now - c["at"]).total_seconds() < RESULT_GRACE_S:
                continue
            if running and c["at"] >= last["start"]:
                continue
            total += 1
            if c["state"] == "unknown":
                lost.append((S, c, (end - c["at"]).total_seconds() < RESULT_GRACE_S))
    if len(lost) >= 3 and len(lost) / total >= 0.05:
        def label(c):
            return f"MCP {c['mcp']}" if c["mcp"] else c["name"]

        top = ", ".join(f"{k} — {v}" for k, v in collections.Counter(label(c) for _, c, _ in lost).most_common(4))
        per = collections.Counter(S["sid"] for S, _, _ in lost)
        at_end = sum(1 for *_, e in lost if e)
        what = (f"В {len(per)} сессиях Codex, начатых при работающих хуках, у {len(lost)} из {total} вызовов нет PostToolUse "
                f"({len(lost) / total * 100:.0f} %), из них в последние 2 минуты своей сессии — {at_end} (возможен обрыв хода). "
                f"Чаще всего: {top}. Сессии, начатые до установки хуков ({len(codex) - len(under)}), не считаются — "
                "они в «Ограничениях данных».")
        ev, seen = [], set()
        for S, c, _ in sorted(lost, key=lambda x: x[1]["at"], reverse=True):      # по сессии — её последний вызов
            if S["sid"] not in seen and len(ev) < 8:
                seen.add(S["sid"])
                ev.append({"sid": S["sid"], "line": c.get("_line"), "at": T.iso(c["at"]), "text": f"нет PostToolUse · {label(c)}"})
        ev = T.newest(ev, 8)
        out.append(_finding(
            "codex-post-lost", "warn", "Найти, почему Codex не присылает PostToolUse для части вызовов", what, ev, sorted(per),
            impact={"value": f"{len(lost)} вызовов", "label": "без результата"}, impact_by_session=dict(per), impact_unit="вызовов",
            where="Codex → хук PostToolUse; ~/.codex/hooks.json — как его пишет hottell install (install.go, codexEvents)",
            snip=("Диагностика: в новой сессии Codex вызвать инструмент из списка выше и сравнить в otel_logs "
                  "countIf(Body='agent.hook.PreToolUse') и countIf(Body='agent.hook.PostToolUse') по его tool_use_id. "
                  "Post нет — проверить, вызывает ли Codex PostToolUse для такого инструмента. Post есть в debug-логе hottell, "
                  "но нет в ClickHouse — искать потерю в спуле или дрейнере."),
            readiness="needs_spec",
            alternative_causes=["Codex не вызывает PostToolUse для части инструментов (MCP-коннекторы и т. п.).",
                                "Вызов оборван: ход прерван или окно закрыто до результата.",
                                "Потеря событий в спуле или дрейнере hottell."],
            verification="В новых сессиях доля вызовов без PostToolUse — меньше 5 %.",
            scope="collection"))
    # (b) Codex native OTel without session linkage
    api_total = sum(int(r.get("n") or 0) for r in codex_otel if r.get("e") == "codex.api_request")
    api_linked = sum(int(r.get("n") or 0) for r in codex_otel if r.get("e") == "codex.api_request" and _truthy(r.get("linked")))
    endpoints = collections.Counter()
    for r in codex_otel:
        if r.get("e") == "codex.api_request":
            endpoints[r.get("endpoint") or "—"] += int(r.get("n") or 0)
    linked_events = sum(int(r.get("n") or 0) for r in codex_otel if _truthy(r.get("linked")))
    all_events = sum(int(r.get("n") or 0) for r in codex_otel)
    codex_sids = [S for S in sessions if S["agent"] == "codex"]
    linked_sessions = [S for S in codex_sids if any(a["src"] == "codex_sse" for a in S["api"])]
    # Сессии, где токены за ход дал журнал через hottell, расход видят и без OTel — карточка о них не говорит.
    covered = [S for S in codex_sids if any(a["src"] == "hook_transcript" for a in S["api"])]
    all_codex = codex_sids
    codex_sids = [S for S in codex_sids if S not in covered]
    missing = [S for S in codex_sids if S not in linked_sessions]
    if missing and all_events:
        ep = ", ".join(f"{k} — {v}" for k, v in endpoints.most_common(3))
        what = (f"Нативный OTel Codex: {all_events} событий, из них с conversation.id — {linked_events}. "
                f"codex.api_request — {api_total} (с conversation.id — {api_linked}; endpoint: {ep or '—'}), токенов в них нет. "
                f"Токены с привязкой к сессии (codex.sse_event response.completed) есть только у {len(linked_sessions)} из "
                f"{len(all_codex)} сессий Codex"
                + (f"; ещё у {len(covered)} токены за ход взяты из журнала сессии через hottell" if covered else "")
                + f". У остальных {len(missing)} токены, число ответов модели и стоимость здесь null.")
        # время — последнее событие сессии: длинная сессия, начатая до периода, в периоде тоже без токенов
        last = {S["sid"]: (S["events"][-1]["line"], S["events"][-1]["at"]) if S["events"] else (1, T.iso(S["start"]))
                for S in missing}
        ev = T.newest([{"sid": S["sid"], "line": last[S["sid"]][0], "at": last[S["sid"]][1],
                        "text": "у этой сессии в OTel нет токенов с conversation.id"} for S in missing], 5)
        out.append(_finding(
            "codex-otel-unlinked", "warn", "Связать нативный OTel Codex с сессиями", what, ev, [S["sid"] for S in missing],
            impact={"value": f"{len(missing)} сессий", "label": "без токенов и стоимости"},
            impact_by_session={S["sid"]: 1 for S in missing}, impact_unit="сессий",
            where="~/.codex/config.toml → [otel], [otel.exporter.otlp-http]; сервисы Codex Desktop / codex-app-server",
            snip=("Диагностика: в одной короткой сессии Codex Desktop проверить, какие события codex.* приходят с conversation.id "
                  "и есть ли codex.sse_event с kind=response.completed от codex-app-server; сравнить с документацией Codex по OTel."),
            readiness="needs_spec",
            alternative_causes=["codex-app-server (десктоп) не отправляет в OTel события с токенами ответа модели.",
                                "События с токенами приходят без conversation.id.",
                                "Экспорт логов включён только у части процессов Codex."],
            verification="У новой сессии Codex есть события с её conversation.id и токенами ответа.",
            scope="collection"))
    # (c) Codex Bash results carry no exit code
    bash = [c for S in codex for c in S["calls"] if c["tool"] == "Bash" and c["post_at"] is not None]
    no_code = [c for c in bash if c["state"] == "done"]
    if len(no_code) >= 3:
        ev = []
        for S in codex:
            c = next((c for c in reversed(S["calls"]) if c["state"] == "done" and c["tool"] == "Bash"), None)
            if c:
                ev.append({"sid": S["sid"], "line": c.get("_line"), "at": T.iso(c["at"]),
                           "text": "PostToolUse Bash: в tool_response только вывод команды, кода выхода нет"})
        ev = T.newest(ev, 5)
        out.append(_finding(
            "codex-bash-exit", "warn", "Получать код выхода Bash в хуке PostToolUse Codex",
            (f"У {len(no_code)} из {len(bash)} результатов Bash в сессиях Codex tool_response — только текст вывода, без кода "
             f"выхода и признака ошибки (в payload хука нет отдельного поля). Ошибки команд Codex поэтому не попадают в errors, "
             f"retry и thrash; такие вызовы считаются выполненными с неизвестным исходом."),
            ev, sorted({S["sid"] for S in codex if any(c in no_code for c in S["calls"])}),
            impact={"value": f"{len(no_code)} результатов", "label": "без кода выхода"},
            impact_by_session=dict(collections.Counter(c["_sid"] for c in no_code)), impact_unit="результатов",
            where="Хук PostToolUse Codex: поле tool_response (hottell/hook.go передаёт payload как есть)",
            snip=("Диагностика: для одной команды с заведомым exit 1 сравнить payload PostToolUse (debug_log hottell) с записью "
                  "CommandExecution в rollout той же сессии; если кода в хуке нет — брать его из rollout по tool_use_id."),
            alternative_causes=["Codex не передаёт код выхода в хук PostToolUse для Bash.",
                                "Код выхода есть в другом событии (не в PostToolUse), которое hottell не регистрирует."],
            verification="У результатов Bash в новых сессиях Codex виден код выхода.",
            readiness="needs_spec", scope="collection"))
    return out


COMPACT_MANY = 3
COMPACT_CLOSE_MIN = 10.0
REREAD_CARD_MIN = 3    # эпизодов перечитывания, чтобы предложить проверить инструкцию
HANDOFF_CANDIDATE = ("Кандидат правки — только если разбор подтвердит, что после сжатия терялось нужное:\n"
                     "1. Когда задача закончена или было 2–3 сжатия, попросить агента записать состояние в docs/handoff.md: "
                     "что сделано, что дальше, какие файлы и команды.\n"
                     "2. Продолжить в новой сессии с репликой «продолжи по docs/handoff.md».")
REREAD_CANDIDATE = ("Кандидат правки — только если в AGENTS.md/CLAUDE.md действительно есть требование перечитывать этот файл: "
                    "«файл целиком — один раз за сессию и после сжатия контекста; перед решением — только нужный раздел».")


def _short_path(path):
    """parent/basename: README.md из разных папок не сливаются в одно имя."""
    parent = os.path.basename(os.path.dirname(path))
    return f"{parent}/{os.path.basename(path)}" if parent else os.path.basename(path)


def work_findings(users):
    """Гипотезы о вашей работе (сессии kind != system): у каждой — что проверить до правки и как мерить пользу."""
    out = []
    # (a) сжатия в длинных сессиях: число сжатий само по себе не доказывает потерю деталей
    longs = []
    for S in users:
        at = [r["at"] for r in S["compacts"]]
        close = [T.minutes(a, b) for a, b in zip(at, at[1:]) if T.minutes(a, b) <= COMPACT_CLOSE_MIN]
        if len(at) >= COMPACT_MANY or close:
            longs.append((S, close))
    if longs:
        n = sum(len(S["compacts"]) for S, _ in longs)
        S0, close0 = max(longs, key=lambda x: len(x[0]["compacts"]))
        subs = len({c["aid"] for c in S0["calls"] if c["aid"]})
        what = (f"Сессий с тремя и более сжатиями контекста или сжатиями подряд — {len(longs)}, сжатий в них — {n}. "
                f"Больше всего у {T.short_id(S0['sid'])}: {T.minutes(S0['start'], S0['end']) / 60:.1f} ч, вызовов — {len(S0['calls'])}, "
                f"субагентов — {subs}, сжатий — {len(S0['compacts'])}"
                + (f", два сжатия с разницей {min(close0):.0f} мин" if close0 else "")
                + ". Мешало ли это работе, по событиям не видно — нужен разбор эпизодов.")
        ev = T.newest([{"sid": S["sid"], "line": r.get("_line"), "at": T.iso(r["at"]),
                        "text": f"сжатие {k + 1}/{len(S['compacts'])} ({r.get('trig') or 'триггер не указан'})"}
                       for S, _ in longs for k, r in enumerate(S["compacts"])], 8)
        out.append(_finding(
            "work-long-session", "warn", "Проверить, мешает ли сжатие контекста в длинных сессиях", what, ev,
            sorted(S["sid"] for S, _ in longs),
            impact={"value": f"{n} сжатий", "label": "в длинных сессиях"},
            impact_by_session={S["sid"]: len(S["compacts"]) for S, _ in longs}, impact_unit="сжатий",
            where="Привычка работы с длинными задачами", snip=HANDOFF_CANDIDATE, kind="habit", pattern_id="compact",
            preconditions=["Открыть эпизоды после сжатий: перечитывал ли агент уже прочитанное, переспрашивал ли, повторял ли сделанное.",
                           "Найти сопоставимые задачи без сжатий и сравнить исход и стоимость."],
            alternative_causes=["Задача одна и большая — сжатия неизбежны и не мешают.",
                                "Сжатие вызывают большие ответы инструментов, а не длина работы."],
            verification=("На сопоставимых задачах после правки исход не хуже, стоимость не выше, повторов после сжатия меньше. "
                          "Одно уменьшение числа сжатий пользой не считается: его даёт простое дробление работы.")))
    # (b) перечитывание по строгому правилу T4
    rr = [(S, it) for S in users for it in S["friction"]["reread"]]
    if len(rr) >= REREAD_CARD_MIN:
        files = collections.defaultdict(lambda: {"extra": 0, "sessions": set()})
        for S, it in rr:
            f = files[it["path"]]
            f["extra"] += it["extra"]
            f["sessions"].add(S["sid"])
        top = sorted(files.items(), key=lambda kv: -kv[1]["extra"])[:4]
        per = collections.Counter()
        for S, it in rr:
            per[S["sid"]] += it["extra"]
        ktok = sum(it["bytes"] for _, it in rr) / 4 / 1000
        what = ("Один агент перечитывал то же место файла и получал тот же ответ, без сжатия и правки между чтениями: "
                f"эпизодов — {len(rr)}, сессий — {len(per)}, лишних чтений — {sum(per.values())} (≈ {ktok:.0f} тыс. токенов). "
                "Чаще всего: " + "; ".join(f"{_short_path(path)} — {v['extra']} в {len(v['sessions'])} сес." for path, v in top)
                + ".")
        out.append(_finding(
            "work-reread", "warn", "Проверить, требует ли инструкция перечитывать файлы целиком", what,
            T.newest([_plain(it["evidence"][-1]) for _, it in rr], 8), sorted(per),
            impact={"value": f"{sum(per.values())} чтений", "label": "лишних"}, impact_by_session=dict(per), impact_unit="чтений",
            where="AGENTS.md / CLAUDE.md проектов этих сессий", snip=REREAD_CANDIDATE, kind="project_rule", pattern_id="reread",
            preconditions=["Найти в инструкциях требование перечитывать эти файлы.",
                           "Открыть эпизоды: нужен ли был файл целиком или хватило бы раздела."],
            alternative_causes=["Агент забыл содержимое после длинной цепочки вызовов — инструкция ни при чём.",
                                "Проверка перед важным шагом — повторное чтение оправдано."],
            verification="На сопоставимых задачах после правки лишних чтений этих файлов вдвое меньше, исход не хуже."))
    # (c) остальные детекторы — «разобрать эпизоды»
    meta = {k: (n, sev) for k, n, sev, _ in T.FRICTION_META}
    for key in ("retry", "thrash", "mcpfail", "coldcache", "wait"):
        occ = [(S, it) for S in users for it in S["friction"].get(key, [])]
        if len(occ) < 3:
            continue
        name, sev = meta[key]
        per = collections.Counter(S["sid"] for S, _ in occ)
        out.append(_finding(
            f"friction-{key}", sev, f"Разобрать эпизоды «{name.lower()}»",
            f"Детектор «{name}» сработал {len(occ)} раз в {len(per)} сессиях. Правило: {LIVE_HOW.get(key) or dict((k, h) for k, _, _, h in T.FRICTION_META)[key]}",
            T.newest([_plain(it["evidence"][-1]) for _, it in occ], 8), sorted(per),     # последнее событие эпизода
            impact={"value": f"{len(occ)} эпизодов", "label": "за окно данных"}, impact_by_session=dict(per), impact_unit="эпизодов",
            snip="Открыть эпизоды по ссылкам и решить, повторяется ли причина; правку описывать только после разбора.",
            preconditions=["Открыть эпизоды по ссылкам и проверить, повторяется ли одна и та же причина.",
                           "Убедиться, что эпизоды не объясняются неполным сбором данных (блок «Здоровье сбора данных»)."],
            pattern_id=key, kind="habit"))
    return out


def build_findings(sessions, codex_otel, now):
    return collection_findings(sessions, codex_otel, now) + work_findings([S for S in sessions if S["kind"] != "system"])


def _started_under_hooks(S):
    first_call = S["calls"][0]["at"] if S["calls"] else None
    return any((r.get("source") or "") in ("startup", "resume") and (first_call is None or r["at"] <= first_call)
               for r in S["session_starts"])


def _truthy(v):
    return v in (1, True, "1", "true", "True")


# --------------------------------------------------------------------------- dataset


GENERAL_GAPS = [
    "Живой вариант: только ClickHouse локального стенда (Hooks hottell + нативный OTel). Rollout, Deep-разборы и тесты не читаются: "
    "задания, исходы, 13 проверок, коррекции = пусто/null.",
    "line в ленте — порядковый номер события внутри сессии (1..N): журнала сессии здесь нет, номер строки rollout неизвестен.",
    "calls — PreToolUse (дубли по tool_use_id отброшены); unknown_results — вызовы без PostToolUse и без tool_result Claude.",
    "Ошибка вызова: tool_result success=false (Claude), «Exit code: N≠0» в начале ответа (apply_patch, exec_command), "
    "isError=true, error/interrupted в JSON-ответе. У Bash в Codex кода выхода в хуке нет — такие вызовы «выполнены, исход неизвестен».",
    "Codex: токены, число ответов модели и стоимость по сессиям не восстанавливаются — codex.api_request в OTel без "
    "conversation.id (это запросы /models); где есть codex.sse_event с conversation.id, токены взяты из него, стоимость — по условной цене.",
    "Claude Code: токены и cost_usd — из событий api_request (собственная оценка Claude Code, не счёт); вход = input + cache_read + "
    "cache_creation, кэш = cache_read; токены рассуждений не сообщаются (null).",
    "active_min: Claude — метрика claude_code.active_time.total (type=cli); Codex — сумма ходов от первого события/реплики до Stop, "
    "ход без Stop — до последнего своего события. user_min — паузы «ответ агента или его остановка на вопросе → ваша реплика», каждая ≤ 30 мин "
    "(длиннее — отсутствие, 0); у Claude метрики type=user нет.",
    "Длительность вызова: Claude — duration_ms из хука/tool_result; Codex — разница времени хуков PreToolUse и PostToolUse.",
    "Коммиты и PR у Codex считаются только при признаке успеха в выводе (строка «[ветка sha]» у git commit, ссылка …/pull/N у "
    "gh pr create); у Claude — метрики commit.count, pull_request.count, lines_of_code.count.",
    "otel_traces не используются: у спанов Codex нет id сессии, длительности инструментов Claude уже есть в tool_result.",
    "Имена инструментов Codex в хуках склеены с пространством имён (collaborationspawn_agent, clockcurr_time, webrun) — "
    "в интерфейсе показаны как collaboration.spawn_agent и т. п.",
]


def build_dataset(raw, url="http://127.0.0.1:8123", database="otel", now=None):
    now = now or dt.datetime.now(dt.timezone.utc)
    groups, dropped, empty = group_sessions(raw.get("hooks") or [])
    claude_by = collections.defaultdict(list)
    for e in raw.get("claude_logs") or []:
        e = dict(e)
        e["at"] = parse_ch_ts(e.get("ts"))
        if e["at"] is not None:
            claude_by[e.get("sid") or ""].append(e)
    metrics_by = collections.defaultdict(list)
    for m in raw.get("claude_metrics") or []:
        metrics_by[m.get("sid") or ""].append(m)
    sse_by = collections.defaultdict(list)
    for e in raw.get("codex_sse") or []:
        e = dict(e)
        e["at"] = parse_ch_ts(e.get("ts"))
        if e["at"] is not None:
            sse_by[e.get("sid") or ""].append(e)

    built = []
    for (agent, sid), rows in sorted(groups.items(), key=lambda kv: kv[1][0]["at"]):
        S = build_session(agent, sid, rows, claude=claude_by.get(sid) if agent == "claude" else None,
                          metrics=metrics_by.get(sid) if agent == "claude" else None,
                          sse=sse_by.get(sid) if agent == "codex" else None, now=now)
        for c in S["calls"]:
            c["_sid"] = sid
        built.append(S)
    fields = [session_fields(S) for S in built]
    timelines = {S["sid"]: session_timeline(S) for S in built}

    gaps = list(GENERAL_GAPS)
    if dropped or empty:
        parts = [f"{k}: {v} событий" for k, v in dropped.items()]
        if empty:
            parts.append(f"{len(empty)} сессий без реплик и вызовов ({', '.join(T.short_id(sid) for _, sid in empty[:8])})")
        gaps.append("Отброшено: " + "; ".join(parts) + ".")
    other_claude = sorted(sid for sid in claude_by if sid and sid not in {S["sid"] for S in built if S["agent"] == "claude"})
    if other_claude:
        n_api = sum(1 for sid in other_claude for e in claude_by[sid] if e["e"] == "api_request")
        gaps.append(f"Claude Code OTel: {len(other_claude)} session.id без событий хуков ({n_api} api_request) — не показаны.")
    for S, f in zip(built, fields):
        short = T.short_id(S["sid"])
        if S["partial"]:
            since = S["first_hook"].strftime("%Y-%m-%d %H:%M UTC") if S["first_hook"] else "?"
            created = S["created"].strftime("%Y-%m-%d %H:%M UTC") if S["created"] else "раньше"
            gaps.append(f"{short}: записан только хвост с {since} — сессия создана {created}, хуков тогда не было или она продолжена (resume).")
        unpaired = sum(1 for c in S["calls"] if c["state"] == "unknown")
        if unpaired:
            gaps.append(f"{short}: {unpaired} из {len(S['calls'])} вызовов без PostToolUse — результат и длительность неизвестны.")
        done = sum(1 for c in S["calls"] if c["state"] == "done")
        if done:
            gaps.append(f"{short}: у {done} вызовов результат записан без признака успеха/ошибки (Bash у Codex и т. п.).")
        if S["dup_pre"]:
            gaps.append(f"{short}: {S['dup_pre']} повторных PreToolUse с тем же tool_use_id отброшены.")
        if S["commit_unconfirmed"]:
            gaps.append(f"{short}: {S['commit_unconfirmed']} git commit / gh pr create без признака успеха в выводе — не посчитаны.")
        if S["agent"] == "codex" and f["sources"]["otel"] == "linked":
            gaps.append(f"{short}: токены — из {len(S['api'])} событий codex.sse_event с conversation.id; полнота не проверяется, "
                        f"стоимость — по условной цене API.")
        open_turns = [t for t in S["turns"] if t["state"] == "open"]
        if open_turns:
            gaps.append(f"{short}: {len(open_turns)} ход(ов) без Stop — длительность до их последнего события.")
    snaps = [S for S in built if S["snapshot"]]
    if snaps and any(not S["snapshot"].get("complete") for S in snaps):
        gaps.append("skills.available — из hottell.skill_snapshot, который помечен complete=false: список может быть неполным.")
    if not snaps:
        gaps.append("hottell.skill_snapshot не найден — skills.available = null.")

    models = sorted({a["model"] for S in built for a in S["api"] if a["src"] in ("codex_sse", "hook_transcript") and a["model"]})
    pricing = {"basis": "otel_reported",
               "note": ("Claude Code: стоимость — собственная оценка Claude Code (cost_usd в событиях api_request), не счёт. "
                        "Codex: токены по сессиям в OTel не связаны. Где их дал журнал сессии через hottell (Stop: токены за ход) "
                        "или codex.sse_event с conversation.id — оценка по условной цене API, одинаковой для всех моделей Codex; "
                        "иначе стоимость null. Фактически — подписки."),
               "assumed": dict(T.ASSUMED_PRICE),
               "models": {m: dict(T.ASSUMED_PRICE) for m in models}}
    starts = [f["start"] for f in fields if f["start"]]
    ends = [f["end"] for f in fields if f["end"]]
    dataset = {
        "schema_version": 1, "variant": "live", "title": TITLE,
        "generated_at": T.iso(now), "host": socket.gethostname(),
        "window": {"from": min(starts) if starts else None, "to": max(ends) if ends else None},
        "source": {"clickhouse": url, "database": database},
        "pricing": pricing,
        "sessions": fields,
        "tools": aggregate_tools(built),
        "mcp": aggregate_mcp(built),
        "commands": aggregate_commands(built, fields),
        "permissions": aggregate_permissions(built),
        "friction": aggregate_friction(built),
        "skills": aggregate_skills(built),
        "findings": build_findings(built, raw.get("codex_otel") or [], now),
        "done": [],
        "checks_catalog": [],
        "gaps": list(dict.fromkeys(gaps)),
    }
    return dataset, timelines


# --------------------------------------------------------------------------- output


def ensure_dir(path):
    path.mkdir(parents=True, exist_ok=True)
    os.chmod(path, 0o700)


def write_json(path, obj):
    fd, tmp = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            json.dump(obj, fh, ensure_ascii=False, separators=(",", ":"))
            fh.write("\n")
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, path)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)
    os.chmod(path, 0o600)


_SID_FILE = re.compile(r"^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}\.json$")


def write_output(out, dataset, timelines):
    out = Path(out)
    ensure_dir(out)
    ensure_dir(out / "sessions")
    for sid, tl in timelines.items():
        write_json(out / "sessions" / f"{sid}.json", tl)
    for p in (out / "sessions").iterdir():
        if _SID_FILE.match(p.name) and p.name[:-5] not in timelines:
            p.unlink()
    write_json(out / "dataset.json", dataset)


def mark_full_history(dataset, reports_dataset):
    """Отметить живые сессии, полная история которых есть в версии «Разобранные сессии».

    Из датасета разборов читается только список id сессий — не содержимое. Нет файла — ничего не отмечается."""
    try:
        with open(reports_dataset, encoding="utf-8") as fh:
            ids = {s.get("id") for s in json.load(fh).get("sessions") or []}
    except (OSError, ValueError, AttributeError):
        ids = set()
    both = []
    for s in dataset["sessions"]:
        s["full_history"] = s["id"] in ids
        if s["full_history"]:
            both.append(s["id"])
    dataset["also_in_reports"] = sorted(both)
    return dataset


def main(argv=None, query=None):
    ap = argparse.ArgumentParser(description="Живой датасет hottell UI v3: только Hooks + OTel из локального ClickHouse.")
    ap.add_argument("--clickhouse", default="http://127.0.0.1:8123")
    ap.add_argument("--database", default="otel")
    ap.add_argument("--since", default=None, help="ISO-время начала окна (UTC)")
    ap.add_argument("--until", default=None, help="ISO-время конца окна (UTC)")
    ap.add_argument("--out", default=str(REPO / "local-data" / "ui-live"))
    ap.add_argument("--reports-dataset", default=str(REPO / "local-data" / "ui" / "dataset.json"),
                    help="датасет версии «Разобранные сессии»: берутся только id, чтобы дать ссылку на полную историю")
    args = ap.parse_args(sys.argv[1:] if argv is None else argv)
    query = query or ClickHouse(args.clickhouse, args.database)
    try:
        raw = fetch(query, args.since, args.until)
    except ClickHouseError as exc:
        print(f"ошибка: {exc}", file=sys.stderr)
        return 2
    now = parse_ch_ts(args.until) if args.until else None          # фиксированное окно: «сейчас» — его конец
    dataset, timelines = build_dataset(raw, args.clickhouse, args.database, now=now)
    mark_full_history(dataset, args.reports_dataset)
    write_output(Path(args.out), dataset, timelines)
    print(f"hottell ui (live): {Path(args.out) / 'dataset.json'}")
    for s in dataset["sessions"]:
        cost = f"${s['cost_usd']:.2f}" if s["cost_usd"] is not None else "—"
        print(f"  {s['short']} {s['agent']:6} {s['project'] or '?':24.24} реплик {s['prompts']}, вызовов {s['calls']} "
              f"(ошибок {s['errors']}, без результата {s['unknown_results']}), {cost}, hooks={s['sources']['hooks']}")
    print(f"  выводов {len(dataset['findings'])}, пробелов {len(dataset['gaps'])}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
