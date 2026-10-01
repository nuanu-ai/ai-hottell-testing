"""Telemetry side of the hottell UI v3 builder.

Parses one raw Codex rollout (``~/.codex/sessions/**/rollout-*-<id>.jsonl``)
into the Session core fields and the per-session timeline of ``ui/CONTRACT.md``,
and turns several parsed sessions into the cross-session aggregates (tools, mcp,
commands, permissions, friction, skills).

Standard library only. Nothing here calls a model: every number is counted from
records in the file. A value that cannot be determined is ``None`` and the
reason goes to ``gaps``. Texts that leave this module are redacted and truncated.
"""

from __future__ import annotations

import bisect
import collections
import datetime as dt
import hashlib
import json
import functools
import math
import os
import re
import shlex
from pathlib import Path

ASSUMED_PRICE = {"input": 1.25, "cached": 0.125, "output": 10.0}   # $ за 1 млн токенов
PRICING = {
    "basis": "api_list_price_estimate",
    "note": ("Оценка по условной цене API: одна и та же цена (вход 1.25 $, кэш 0.125 $, выход 10 $ за 1 млн токенов) "
             "применяется ко всем моделям Codex в датасете. Фактически — подписка Codex, счёт по токенам "
             "не выставлялся."),
    "assumed": dict(ASSUMED_PRICE),
    # заполняется при разборе: каждая встреченная модель получает ту же условную цену
    "models": {},
}

AWAY_MIN = 30.0          # пауза длиннее — «вас не было», в user_min не идёт
COLD_PAUSE_MIN = 5.0     # coldcache: пауза перед репликой
COLD_RATIO = 0.5         # coldcache: доля кэша в первом запросе хода
RETRY_WINDOW = 10
RETRY_MIN = 3
THRASH_MIN = 3
MCPFAIL_MIN = 3
REREAD_MIN = 3  # одно и то же место файла
MCP_MERGE_S = 120
LONG_TURN_H = 3
MASK = "[скрыто]"
_PASSIVE = {"thread_settings_applied", "world_state", "turn_context", "session_meta", "bad", "token_count"}

FRICTION_META = [
    ("retry", "Повтор с той же ошибкой", "bad",
     "Один и тот же вызов (одинаковый вход) ≥ 3 раз в окне 10 вызовов, все с ошибкой, без правок между; "
     "опрос процессов (write_stdin, sleep) исключён."),
    ("thrash", "Цикл правка → тест", "bad",
     "≥ 3 провала тестовой команды подряд (pytest, xcodebuild test, npm test …), между каждыми двумя — правка файла."),
    ("mcpfail", "Сбой MCP", "bad", "≥ 3 ошибок одного MCP-сервера за сессию (статус failed или isError)."),
    ("coldcache", "Холодный кэш", "warn",
     "Ваша реплика после паузы > 5 мин от последнего ответа модели; первый запрос после неё читает из кэша < 50 % входа. "
     "Стоимость — некэшированная часть этого запроса × (цена входа − цена кэша)."),
    ("compact", "Сжатие контекста", "warn", "Запись `compacted` в rollout (автосжатие посреди хода)."),
    ("reread", "Перечитывание файлов", "warn",
     "Одно и то же место файла (тот же диапазон строк или файл целиком) прочитано ≥ 3 раз за сессию; "
     "чтение большого файла кусками подряд не считается (parsed_cmd type=read + диапазон из команды)."),
    ("wait", "Агент ждал вас", "info", "Вызов request_user_input*; ожидание — до вашей следующей реплики."),
    ("abort", "Прерванный ход", "info", "Событие turn_aborted."),
    ("correction", "Ваша коррекция", "warn", "Ход, размеченный как correction в тестовом разборе (review.json)."),
]
FRICTION_KEYS = [k for k, *_ in FRICTION_META]

# --------------------------------------------------------------------------- time


def parse_ts(value):
    """ISO string or epoch (s or ms) -> aware UTC datetime; None stays None."""
    if value is None or value == "":
        return None
    if isinstance(value, (int, float)):
        v = value / 1000.0 if value > 1e11 else float(value)
        return dt.datetime.fromtimestamp(v, dt.timezone.utc)
    s = str(value).strip()
    if s.endswith("Z"):
        s = s[:-1] + "+00:00"
    try:
        d = dt.datetime.fromisoformat(s)
    except ValueError:
        return None
    if d.tzinfo is None:
        d = d.replace(tzinfo=dt.timezone.utc)
    return d.astimezone(dt.timezone.utc)


def iso(d):
    if d is None:
        return None
    d = d.astimezone(dt.timezone.utc)
    return d.strftime("%Y-%m-%dT%H:%M:%S.") + f"{d.microsecond // 1000:03d}Z"


def ms_of(d):
    return None if d is None else int(round(d.timestamp() * 1000))


def minutes(a, b):
    if a is None or b is None:
        return None
    return (b - a).total_seconds() / 60.0


# --------------------------------------------------------------------------- text

_SECRET_KV = re.compile(
    r"(?i)\b(token|secret|password|passwd|api[_-]?key|authorization)(\s*[:=]\s*)"
    r"(?:(?:bearer|basic|token)\s+)?(\"[^\"]*\"|'[^']*'|\S+)")
_BEARER = re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{6,}")
_SK = re.compile(r"\bsk-[A-Za-z0-9_-]{10,}")
_VENDOR = re.compile(r"\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16})\b")
_PEM = re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)", re.S)
_BLOB = re.compile(r"[A-Za-z0-9+/=_-]{32,}")
_UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", re.I)
_HOME = re.compile(r"/Users/[^/\s\"']+")


def _looks_like_blob(part):
    core = part.strip("=-_+")
    if len(core) < 32 or _UUID.fullmatch(core):
        return False
    if re.fullmatch(r"[A-Fa-f0-9]+", core):
        return True
    if max(len(p) for p in core.split("-")) < 20:   # kebab-case names, dated file names
        return False
    if not any(c.isdigit() for c in core):
        return False
    if any(c.isupper() for c in core) and any(c.islower() for c in core):
        return True
    return sum(c.isdigit() for c in core) / len(core) >= 0.25


def _blob_sub(m):
    parts = m.group(0).split("/")
    return "/".join(MASK if _looks_like_blob(p) else p for p in parts)


def redact(text):
    """Mask things that look like secrets. Applied to every text that leaves the parser."""
    if text is None:
        return None
    t = str(text)
    t = _PEM.sub(MASK, t)
    t = _SECRET_KV.sub(lambda m: m.group(1) + m.group(2) + MASK, t)
    t = _BEARER.sub("Bearer " + MASK, t)
    t = _SK.sub(MASK, t)
    t = _VENDOR.sub(MASK, t)
    t = _BLOB.sub(_blob_sub, t)
    return t


def clean(text, limit):
    """Redact, shorten home paths, collapse whitespace, truncate to ``limit`` chars."""
    if text is None:
        return None
    t = redact(text)
    t = _HOME.sub("~", t)
    t = " ".join(t.split())
    if len(t) > limit:
        t = t[: max(1, limit - 1)].rstrip() + "…"
    return t


def short_id(sid):
    """Короткий id сессии: первые 8 и последние 4 знака (01a0f57b…4b74). У uuid7 первые 8 знаков —
    время с шагом ~65 с, и сессии, начатые в одну минуту, по ним не различить."""
    s = str(sid or "")
    return s if len(s) <= 12 else f"{s[:8]}…{s[-4:]}"


def fmt_ktok(n):
    if n is None:
        return "—"
    if n >= 1000:
        return f"{round(n / 1000):,}".replace(",", " ") + " тыс."
    return str(int(n))


def fmt_dur(seconds):
    if seconds is None:
        return None
    if seconds < 90:
        return f"{seconds:.1f} с"
    if seconds < 5400:
        return f"{seconds / 60:.1f} мин"
    return f"{seconds / 3600:.1f} ч"


def fmt_usd(v):
    if v is None:
        return None
    if v >= 0.995:
        return f"${v:.2f}"
    if v >= 0.01:
        return f"${v:.2f}"
    return f"${v:.4f}"


def pct(values, q):
    vals = sorted(v for v in values if v is not None)
    if not vals:
        return None
    k = max(0, min(len(vals) - 1, math.ceil(q * len(vals)) - 1))
    return round(vals[k], 2)


# --------------------------------------------------------------------------- prompts

_REPLY_OPEN = "<send_user_message_question_reply>"


def prompt_text(raw):
    """UserMessage text -> (human text, kind). kind is 'prompt' or 'reply' (answer to request_user_input)."""
    t = raw or ""
    if _REPLY_OPEN in t:
        body = t.split(_REPLY_OPEN, 1)[1].split("</send_user_message_question_reply>", 1)[0].strip()
        answers = []
        try:
            data = json.loads(body)
            for q in data if isinstance(data, list) else [data]:
                if isinstance(q, dict) and q.get("answer") not in (None, ""):
                    a = q["answer"]
                    answers.append(a if isinstance(a, str) else json.dumps(a, ensure_ascii=False))
        except ValueError:
            for a in re.findall(r'"answer"\s*:\s*"((?:[^"\\]|\\.)*)"', body):
                try:
                    answers.append(json.loads('"' + a + '"'))
                except ValueError:
                    answers.append(a)
        return ("ответ на вопрос агента: " + " / ".join(answers)) if answers else "ответ на вопрос агента", "reply"
    if "## My request:" in t:
        return t.rsplit("## My request:", 1)[1].strip(), "prompt"
    t = re.sub(r"<([a-z_-]+)[^>]*>.*?</\1>", " ", t, flags=re.S)
    t = re.sub(r"(?m)^# Files mentioned by the user:.*?(?:\n\s*\n|$)", " ", t, flags=re.S)
    return t.strip(), "prompt"


def _message_text(content):
    out = []
    for part in content or []:
        if isinstance(part, dict) and part.get("type") in ("text", "input_text", "output_text", "Text"):
            out.append(part.get("text") or "")
    return "\n".join(out)


# --------------------------------------------------------------------------- shell commands

_CMD_LIT = re.compile(
    r"""(?<![\w$])["']?cmd["']?\s*:\s*("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|`(?:[^`\\]|\\.)*`)""", re.S)
_HEREDOC = re.compile(r"<<-?\s*(['\"]?)([A-Za-z_][\w]*)\1[^\n]*\n.*?\n\s*\2\b", re.S)
_OPS = {";", "&&", "||", "|", "&", "(", ")", "\n", ";;", "|&", "{", "}"}
_WRAPPERS = {"sudo", "time", "env", "nohup", "command", "exec", "caffeinate"}
_SKIP_PROGS = {"cd", "export", "set", "source", ".", "true", "pushd", "popd", "unset", "ulimit", "trap", "local"}
_SUBCMD = {"git", "gh", "npm", "pnpm", "yarn", "uv", "cargo", "go", "docker", "kubectl", "brew", "swift",
           "make", "pip", "pip3", "poetry", "bun", "npx", "launchctl", "defaults", "codex", "tectd", "hottell",
           "open", "xcode-select", "security", "tailscale", "ssh", "node", "deno", "terraform", "helm", "mise"}
_XCB_ACTIONS = ["test-without-building", "build-for-testing", "test", "build", "clean", "archive", "analyze",
                "-version", "-list", "-showsdks", "-showBuildSettings", "-showdestinations"]
_READERS = {"cat", "sed", "head", "tail", "less", "more", "nl", "bat", "rg", "grep", "awk", "view"}
_FLAG_WITH_VALUE = {"-g", "--glob", "-e", "--regexp", "-t", "--type", "-m", "--max-count", "-A", "-B", "-C",
                    "--max-columns", "-f", "--file"}
_SKILL_TOKEN = re.compile(r"[\w./~@+-]*SKILL\.md")


def js_string(lit):
    """Decode a JS string literal captured by _CMD_LIT."""
    if lit.startswith('"'):
        try:
            return json.loads(lit)
        except ValueError:
            pass
    body = lit[1:-1]
    return re.sub(r"\\(.)", lambda m: {"n": "\n", "t": "\t"}.get(m.group(1), m.group(1)), body)


def extract_js_cmds(js):
    """Shell commands passed as ``cmd:"..."`` literals in an exec JS script, in source order."""
    return [js_string(m.group(1)) for m in _CMD_LIT.finditer(js or "")]


def shell_of(command):
    """CommandExecution.command (argv list or string) -> the shell command string."""
    if isinstance(command, list):
        if len(command) >= 3 and os.path.basename(str(command[0])) in ("zsh", "bash", "sh") and command[1] in ("-lc", "-c", "-l"):
            return str(command[-1])
        return " ".join(str(c) for c in command)
    return str(command or "")


def split_segments(cmd):
    """Split a shell command into simple-command token lists (heredoc bodies removed)."""
    cmd = cmd or ""
    segs = _split_cached(cmd) if len(cmd) < 8000 else _split_impl(cmd)
    return [list(seg) for seg in segs]


def _split_impl(cmd):
    text = _HEREDOC.sub(" ", cmd or "")
    try:
        lex = shlex.shlex(text, posix=True, punctuation_chars=";&|()\n")
        lex.whitespace_split = True
        lex.whitespace = " \t\r"
        tokens = list(lex)
    except ValueError:
        tokens = []
        for piece in re.split(r"\|\||&&|;|\||\n", text):
            tokens.extend(piece.split())
            tokens.append(";")
    segs, cur = [], []
    for tok in tokens:
        if tok in _OPS or (tok and set(tok) <= set(";&|()\n")):
            if cur:
                segs.append(cur)
            cur = []
        else:
            cur.append(tok)
    if cur:
        segs.append(cur)
    return tuple(tuple(seg) for seg in segs)


_split_cached = functools.lru_cache(maxsize=2048)(_split_impl)


def _strip_prefix(tokens):
    i = 0
    while i < len(tokens):
        t = tokens[i]
        if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*=.*", t):
            i += 1
            continue
        base = os.path.basename(t)
        if base in _WRAPPERS:
            i += 1
            continue
        if base == "timeout" and i + 1 < len(tokens):
            i += 2
            continue
        break
    return tokens[i:]


def _positional(tokens, start=1):
    out, skip = [], False
    for t in tokens[start:]:
        if skip:
            skip = False
            continue
        if t.startswith("-"):
            if t in ("-C", "-c", "--git-dir", "--work-tree", "-R", "--repo"):
                skip = True
            continue
        out.append(t)
    return out


_WEAK_PROGS = {"[", "[[", "test", "echo", "printf", "sleep", ":"}


def normalize_cmd(cmd):
    """Group a shell command: first meaningful simple command, 1–3 tokens ('git status', 'sed -n', 'rg')."""
    segs = [s for s in (_strip_prefix(x) for x in split_segments(cmd)) if s]
    strong = [s for s in segs if os.path.basename(s[0]) not in _SKIP_PROGS | _WEAK_PROGS and not s[0].startswith("#")]
    for toks in strong or [s for s in segs if os.path.basename(s[0]) not in _SKIP_PROGS]:
        prog = os.path.basename(toks[0])
        if prog in ("python", "python3") or re.fullmatch(r"python3\.\d+", prog):
            if "-m" in toks:
                i = toks.index("-m")
                return f"{prog} -m {toks[i + 1]}" if i + 1 < len(toks) else f"{prog} -m"
            if "-c" in toks or "-" in toks[1:2] or len(toks) == 1:
                return f"{prog} (скрипт)"
            pos = _positional(toks)
            return f"{prog} {os.path.basename(pos[0])}" if pos else prog
        if prog == "xcodebuild":
            for a in _XCB_ACTIONS:
                if a in toks:
                    return f"xcodebuild {a}"
            return "xcodebuild"
        if prog == "xcrun":
            pos = _positional(toks)
            if pos[:1] == ["simctl"] and len(pos) > 1:
                return f"xcrun simctl {pos[1]}"
            return f"xcrun {pos[0]}" if pos else "xcrun"
        if prog == "sed":
            return "sed -n" if "-n" in toks else "sed"
        if prog in _SUBCMD:
            pos = _positional(toks)
            if not pos:
                return prog
            if prog in ("uv", "npm", "pnpm", "yarn", "bun", "gh", "poetry") and len(pos) > 1 and pos[0] in (
                    "run", "pr", "repo", "issue", "release", "auth", "exec", "workflow", "run-script"):
                return f"{prog} {pos[0]} {os.path.basename(pos[1])}"
            return f"{prog} {pos[0]}" if not pos[0].startswith(("/", "~", ".")) else prog
        return prog
    return "(пусто)"


def is_test_cmd(cmd):
    c = cmd or ""
    return bool(re.search(
        r"\b(pytest|py\.test|unittest|jest|vitest|mocha)\b|xcodebuild\b[^\n;&|]*\btest\b|"
        r"\b(npm|pnpm|yarn|bun)\s+(run\s+)?test\b|\bgo\s+test\b|\bcargo\s+test\b|\bswift\s+test\b|"
        r"\bnode\s+--test\b|\btsx\s+--test\b|\bmake\s+(check|test)\b", c))


_SPAN = re.compile(r"sed\s+-n\s+['\"]?(\d+)\s*,\s*(\d+)\s*p")
_HEADTAIL = re.compile(r"\b(head|tail)\s+(?:-n\s*)?-?(\d+)")


def read_span(cmd) -> str:
    """Какое место файла читает команда: «строки a–b», «первые N», «последние N» или «весь файл»."""
    text = cmd if isinstance(cmd, str) else " ".join(map(str, cmd or []))
    m = _SPAN.search(text)
    if m:
        return f"строки {m.group(1)}–{m.group(2)}"
    m = _HEADTAIL.search(text)
    if m:
        return f"{'первые' if m.group(1) == 'head' else 'последние'} {m.group(2)}"
    return "весь файл"


def skill_md_reads(cmd, parsed=None, cwd=None):
    """Paths of SKILL.md files a command reads (cat/sed/head/rg … on …/SKILL.md). Order kept, no dups."""
    found = []

    def add(p):
        if not p:
            return
        p = os.path.expanduser(p)
        if not os.path.isabs(p) and cwd:
            p = os.path.normpath(os.path.join(cwd, p))
        if p not in found:
            found.append(p)

    for item in parsed or []:
        if isinstance(item, dict) and item.get("type") == "read" and str(item.get("path") or "").endswith("SKILL.md"):
            add(item.get("path"))
    for seg in split_segments(cmd):
        toks = _strip_prefix(seg)
        if not toks:
            continue
        prog = os.path.basename(toks[0])
        if prog not in _READERS:
            continue
        pos, skip = [], False
        explicit_script = False
        for t in toks[1:]:
            if skip:
                skip = False
                continue
            if t in _FLAG_WITH_VALUE:
                skip = True
                if t in ("-e", "--regexp", "-f", "--file"):
                    explicit_script = True
                continue
            if t.startswith("-"):
                continue
            pos.append(t)
        if prog in ("rg", "grep", "sed", "awk") and not explicit_script:
            pos = pos[1:]   # first positional is the pattern / script, not a file
        for t in pos:
            if _SKILL_TOKEN.fullmatch(t):
                add(t)
    return found


# --------------------------------------------------------------------------- skills catalogue

_ROOT_RE = re.compile(r"^- `(r\d+)` = `([^`]+)`", re.M)
_SKILL_RE = re.compile(r"^- (\S+?): .*?\(file: (r\d+)/(\S+?SKILL\.md)\)\s*$", re.M)


def parse_skills_block(text):
    """<skills_instructions> body -> {name: absolute SKILL.md path}."""
    roots = dict(_ROOT_RE.findall(text or ""))
    out = {}
    for name, root, rel in _SKILL_RE.findall(text or ""):
        base = roots.get(root)
        out[name] = os.path.join(base, rel) if base else rel
    return out


def skill_source(path):
    p = path or ""
    m = re.search(r"/plugins/cache/([^/]+)/([^/]+)/", p)
    if m:
        return f"плагин {m.group(1)}/{m.group(2)}"
    if "/.codex/skills/.system/" in p:
        return "системные (~/.codex/skills/.system)"
    if "/.codex/skills/" in p:
        return "личные (~/.codex/skills)"
    if "/.agents/skills/" in p:
        return "~/.agents/skills"
    parent = os.path.dirname(os.path.dirname(p))
    return clean(parent, 80) or "неизвестно"


# --------------------------------------------------------------------------- cost


def price_for(model, prices=None):
    """$ per 1M tokens. With the default table every Codex model gets the single assumed price and is
    listed in PRICING['models']; a custom ``prices`` table is used as is (missing model -> None)."""
    if prices is not None and prices is not PRICING["models"]:
        return prices.get(model)
    if not model:
        return ASSUMED_PRICE
    return PRICING["models"].setdefault(model, dict(ASSUMED_PRICE))


def pricing_for(parsed):
    """Pricing block listing exactly the models present in ``parsed`` sessions."""
    models = sorted({a["model"] for P in parsed for a in P["api"] if a["model"]})
    return {"basis": PRICING["basis"], "note": PRICING["note"], "assumed": dict(ASSUMED_PRICE),
            "models": {m: dict(price_for(m) or ASSUMED_PRICE) for m in models}}


def usage_cost(inp, cached, out, model, prices):
    price = price_for(model, prices)
    if not price:
        return None
    unc = max(0, (inp or 0) - (cached or 0))
    return (unc * price["input"] + (cached or 0) * price["cached"] + (out or 0) * price["output"]) / 1e6


# --------------------------------------------------------------------------- parse


def _new_tok():
    return {"input": 0, "cached": 0, "output": 0, "reasoning": 0}


def _call_kind(name, namespace):
    ns = namespace or ""
    name = name or ""
    if ns.startswith("mcp__"):
        return "mcp"
    if name == "exec":
        return "exec"
    if name == "wait" and not ns:          # js-kernel wait for a running exec cell (old format)
        return "poll"
    if name == "write_stdin":
        return "poll"
    if name in ("exec_command", "shell", "local_shell", "container.exec"):
        return "shell"
    if name == "apply_patch":
        return "patch"
    if name.startswith("request_user_input"):
        return "wait"
    if ns == "collaboration" or name in ("spawn_agent", "send_message", "followup_task", "wait_agent", "close_agent",
                                         "list_agents", "interrupt_agent"):
        return "agent"
    if name == "sleep":
        return "sleep"
    return "other"


def _output_text(output):
    if isinstance(output, list):
        return "\n".join(str(p.get("text", "")) if isinstance(p, dict) else str(p) for p in output)
    return "" if output is None else str(output)


def _output_parts(output):
    if isinstance(output, list):
        return [str(p.get("text", "")) if isinstance(p, dict) else str(p) for p in output]
    return [] if output is None else [str(output)]


def _text_start(parts, limit=1500):
    out, n = [], 0
    for p in parts:
        out.append(p[: limit - n])
        n += len(out[-1]) + 1
        if n >= limit:
            break
    return "\n".join(out)


_WALL = re.compile(r"Wall time:?\s*([\d.]+)\s*seconds")
_EXIT_TOP = re.compile(r'(?<!\\)"exit_code"\s*:\s*(-?\d+)')
_RUNNING_TOP = re.compile(r'(?<!\\)"session_id"\s*:\s*\d+')
_PROC_EXIT = re.compile(r"(?:Process exited with code|Exit code:)\s*(-?\d+)")
_PROC_RUNNING = re.compile(r"Process running with session ID")
_CELL_RUNNING = re.compile(r"^Script running with cell ID\s*(\S+)")
_TIMED_OUT = re.compile(r"(?i)js execution timed out|kernel reset|^Script timed out")
_IS_ERROR = re.compile(r'"isError"\s*:\s*true')
_STAT = re.compile(r"(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?")
_PATCH_FILE = re.compile(r"\*\*\* (?:Add|Update|Delete) File: ([^\s\\]+)")
_WAITY = re.compile(r"write_stdin|wait_threads|clock\.sleep|clock__sleep|functions\.wait")


def _inner_chunks(parts):
    """Inner exec_command results in an exec/wait output: [(exit_code|None, wall_s|None, running)]."""
    chunks = []
    for text in parts[1:] if len(parts) > 1 else parts:
        objs = []
        s = text.strip()
        if s.startswith("{") or "\n{" in s:
            try:
                o = json.loads(s)
                objs = [o] if isinstance(o, dict) else []
            except ValueError:
                for ln in s.splitlines():
                    ln = ln.strip()
                    if ln.startswith("{"):
                        try:
                            o = json.loads(ln)
                            if isinstance(o, dict):
                                objs.append(o)
                        except ValueError:
                            pass
        if objs:
            for o in objs:
                if "exit_code" not in o and "session_id" not in o and "wall_time_seconds" not in o:
                    continue
                code = o.get("exit_code") if isinstance(o.get("exit_code"), int) else None
                wall = o.get("wall_time_seconds") if isinstance(o.get("wall_time_seconds"), (int, float)) else None
                chunks.append((code, wall, code is None and "session_id" in o))
        else:  # truncated JSON: fall back to unescaped top-level keys only
            codes = [int(x) for x in _EXIT_TOP.findall(text)]
            chunks.extend((x, None, False) for x in codes)
            if not codes:
                chunks.extend((None, None, True) for _ in _RUNNING_TOP.findall(text))
    return chunks


def _inner_results(parts):
    """exit codes / running flags of inner exec_command results found in an exec output."""
    chunks = _inner_chunks(parts)
    return [c for c, _, _ in chunks if c is not None], sum(1 for _, _, r in chunks if r)


def _shrink(v, limit=500):
    if isinstance(v, str):
        return v if len(v) <= limit else v[:limit]
    if isinstance(v, list):
        return [_shrink(x, limit) for x in v[:20]]
    if isinstance(v, dict):
        return {k: _shrink(x, limit) for k, x in list(v.items())[:40]}
    return v


def _dur_of(d, started, completed):
    if d:
        return (d.get("secs") or 0) + (d.get("nanos") or 0) / 1e9
    if started and completed:
        return (completed - started) / 1000.0
    return None


def _cmd_facts(cmd, output="", parsed=None, cwd=None):
    """Everything derived from the full command text, computed once so only a short copy is kept."""
    commit = pr = False
    for seg in split_segments(cmd):
        toks = _strip_prefix(list(seg))
        if not toks:
            continue
        prog = os.path.basename(toks[0])
        pos = _positional(toks)
        if prog == "git" and pos[:1] == ["commit"]:
            commit = True
        if prog == "gh" and pos[:2] == ["pr", "create"]:
            pr = True
    m = _STAT.search(output or "") if commit else None
    return {"norm": normalize_cmd(cmd), "is_test": is_test_cmd(cmd), "skills": skill_md_reads(cmd, parsed, cwd),
            "commit": commit, "pr": pr, "stat": (int(m.group(2) or 0), int(m.group(3) or 0)) if m else None}


def _api_rec(ln, at, turn, model, u, src, prices):
    rec = {"line": ln, "at": at, "turn": turn, "model": model, "src": src,
           "input": u.get("input_tokens") or 0, "cached": u.get("cached_input_tokens") or 0,
           "output": u.get("output_tokens") or 0, "reasoning": u.get("reasoning_output_tokens") or 0}
    rec["cost"] = usage_cost(rec["input"], rec["cached"], rec["output"], model, prices)
    return rec


_TOK_KEYS = ("input_tokens", "cached_input_tokens", "output_tokens", "reasoning_output_tokens")


def parse_rollout(path, prices=None):
    """Parse one rollout file line by line (raw records are not kept).

    Returns a dict of compact internals; ``session_core``/``session_timeline`` turn it into contract JSON."""
    path = Path(path)
    prices = PRICING["models"] if prices is None else prices
    sha = hashlib.sha256()

    line_dt, line_kind, bad_lines = [], [], []
    meta, tctx = {}, {}
    models = collections.Counter()
    last_model = None
    turns, turn_by_id = [], {}
    cur = None
    prompts, answers, aborts, compactions, cc_items = [], [], [], [], []
    calls, calls_by_id = [], {}
    api = []
    ces, mcp_items, edits, nested, subagent_items = [], [], [], [], []
    skills_available = {}
    # token accounting: token_usage_record is authoritative; token_count fills in responses it does not cover
    pending_tur = collections.deque(maxlen=16)
    prev_total = None
    tc_sum = collections.Counter()
    seg_final = []
    tok_stat = {"tur": 0, "tc_used": 0, "tc_matched": 0, "tc_dup": 0, "resets": 0}
    ln = 0

    with path.open("rb") as fh:
        for ln, raw in enumerate(fh, 1):
            sha.update(raw)
            try:
                rec = json.loads(raw)
            except ValueError:
                bad_lines.append(ln)
                line_dt.append(line_dt[-1] if line_dt else None)
                line_kind.append("bad")
                continue
            if not isinstance(rec, dict):
                bad_lines.append(ln)
                line_dt.append(line_dt[-1] if line_dt else None)
                line_kind.append("bad")
                continue
            at = parse_ts(rec.get("timestamp")) or (line_dt[-1] if line_dt else None)
            line_dt.append(at)
            typ = rec.get("type")
            pl = rec.get("payload") if isinstance(rec.get("payload"), dict) else {}
            rec = None
            sub = pl.get("type")
            kind_name = sub if typ == "event_msg" else typ
            line_kind.append(kind_name)
            if cur is not None and kind_name not in _PASSIVE and kind_name != "task_started" and at is not None:
                cur["last_at"], cur["last_line"] = at, ln

            def turn_of(tid):
                return turn_by_id.get(tid, cur)

            if typ == "session_meta":
                meta = {"cwd": pl.get("cwd"), "provider": pl.get("model_provider"), "id": pl.get("id") or pl.get("session_id")}
            elif typ == "turn_context":
                model = pl.get("model")
                tctx[pl.get("turn_id")] = {"model": model, "approval": pl.get("approval_policy"),
                                           "sandbox": (pl.get("sandbox_policy") or {}).get("type"), "cwd": pl.get("cwd")}
                if model:
                    models[model] += 1
                    last_model = model
            elif typ == "world_state":
                body = (((pl.get("state") or {}).get("host_skills") or {}).get("body")) or ""
                if "SKILL.md" in body:
                    skills_available.update(parse_skills_block(body))
            elif typ == "compacted":
                usage = ((pl.get("latest_token_usage_record") or {}).get("usage") or {})
                compactions.append({"line": ln, "at": at, "turn": cur, "window": pl.get("window_number"),
                                    "ctx_before": usage.get("input_tokens"),
                                    "in_turn": cur is not None and cur["state"] == "open"})
            elif typ == "token_usage_record":
                u = pl.get("usage") or {}
                t = turn_of(pl.get("turn_id"))
                model = (tctx.get(pl.get("turn_id")) or {}).get("model") or last_model
                a = _api_rec(ln, at, t, model, u, "token_usage_record", prices)
                api.append(a)
                pending_tur.append(tuple(u.get(k) or 0 for k in _TOK_KEYS))
                tok_stat["tur"] += 1
                if t is not None:
                    for k in ("input", "cached", "output", "reasoning"):
                        t["tok"][k] += a[k]
            elif typ == "event_msg" and sub == "token_count":
                info = pl.get("info") or {}
                total = info.get("total_token_usage")
                last = info.get("last_token_usage")
                if not total or total == prev_total:
                    tok_stat["tc_dup"] += 1 if total else 0
                    continue
                if prev_total and (total.get("total_tokens") or 0) < (prev_total.get("total_tokens") or 0):
                    tok_stat["resets"] += 1
                    seg_final.append(prev_total)
                prev_total = total
                if not last:
                    continue
                for k in _TOK_KEYS:
                    tc_sum[k] += last.get(k) or 0
                key = tuple(last.get(k) or 0 for k in _TOK_KEYS)
                if key in pending_tur:
                    while pending_tur and pending_tur.popleft() != key:
                        pass
                    tok_stat["tc_matched"] += 1
                    continue
                model = ((tctx.get(cur["turn_id"]) or {}).get("model") if cur else None) or last_model
                a = _api_rec(ln, at, cur, model, last, "token_count", prices)
                api.append(a)
                tok_stat["tc_used"] += 1
                if cur is not None:
                    for k in ("input", "cached", "output", "reasoning"):
                        cur["tok"][k] += a[k]
            elif typ == "event_msg" and sub == "task_started":
                cur = {"turn": len(turns), "turn_id": pl.get("turn_id"), "start": at, "start_line": ln, "end": None,
                       "end_line": None, "dur_ms": None, "state": "open", "prompt_line": None, "prompt": None,
                       "answer": None, "tok": _new_tok(), "last_at": at, "last_line": ln}
                turns.append(cur)
                turn_by_id[pl.get("turn_id")] = cur
            elif typ == "event_msg" and sub in ("task_complete", "turn_aborted"):
                t = turn_by_id.get(pl.get("turn_id"), cur)
                if t is not None:
                    t["end"], t["end_line"], t["state"] = at, ln, sub
                    t["dur_ms"] = pl.get("duration_ms")
                    if t["dur_ms"] is None and t["start"] is not None:
                        t["dur_ms"] = int((at - t["start"]).total_seconds() * 1000)
                    if sub == "task_complete":
                        t["answer"] = (pl.get("last_agent_message") or "")[:2000] or None
                        answers.append({"line": ln, "at": at, "turn": t, "text": t["answer"]})
                    else:
                        aborts.append({"line": ln, "at": at, "turn": t, "reason": pl.get("reason")})
            elif typ == "event_msg" and sub == "item_completed":
                it = pl.get("item") or {}
                ityp = it.get("type")
                completed = pl.get("completed_at_ms")
                dur = _dur_of(it.get("duration") or {}, pl.get("started_at_ms"), completed)
                started = pl.get("started_at_ms") or (int(completed - dur * 1000) if completed and dur else completed)
                if ityp == "UserMessage":
                    text, kind = prompt_text(_message_text(it.get("content")))
                    text = text[:2000]
                    t = turn_of(pl.get("turn_id"))
                    prompts.append({"line": ln, "at": at, "turn": t, "text": text, "kind": kind})
                    if t is not None and t["prompt_line"] is None:
                        t["prompt_line"], t["prompt"] = ln, text
                elif ityp == "CommandExecution":
                    cwd = str(it.get("cwd") or "")
                    if cwd.startswith("file://"):
                        cwd = cwd[len("file://"):]
                    cmd = shell_of(it.get("command"))
                    output = it.get("aggregated_output") or it.get("stdout") or ""
                    parsed = [_shrink(x, 400) for x in (it.get("parsed_cmd") or [])[:20] if isinstance(x, dict)]
                    ce = {"line": ln, "at": at, "turn": turn_of(pl.get("turn_id")), "started_ms": started,
                          "completed_ms": completed, "cmd": cmd[:4000], "cwd": cwd, "exit": it.get("exit_code"),
                          "status": it.get("status"), "dur_s": dur, "parsed": parsed,
                          "stderr": (it.get("stderr") or "")[-2000:], "output": output[-1500:], "pseudo": False}
                    ce.update(_cmd_facts(cmd, output, parsed, cwd or None))
                    ces.append(ce)
                elif ityp == "McpToolCall":
                    res = it.get("result") or {}
                    err_text = _output_text(res.get("content"))[:500] if isinstance(res, dict) else ""
                    failed = it.get("status") == "failed" or (isinstance(res, dict) and res.get("isError") is True)
                    if not err_text and isinstance(it.get("error"), (str, dict)):
                        err_text = str(it.get("error"))[:500]
                    args = it.get("arguments") if isinstance(it.get("arguments"), dict) else {}
                    mcp_items.append({"line": ln, "at": at, "id": it.get("id"), "server": it.get("server"),
                                      "tool": it.get("tool"), "status": it.get("status"), "failed": failed,
                                      "dur_s": dur, "started_ms": started, "completed_ms": completed,
                                      "title": (args.get("title") or "")[:200] or None,
                                      "code": (args.get("code") or "")[:300] or None, "err": err_text,
                                      "turn": turn_of(pl.get("turn_id")), "direct": False})
                elif ityp == "FileChange":
                    edits.append({"line": ln, "at": at, "started_ms": started, "status": it.get("status"),
                                  "files": len(it.get("changes") or {})})
                elif ityp in ("ImageView", "Extension", "WebSearch"):
                    name = {"ImageView": "view_image", "WebSearch": "web.search"}.get(ityp) or it.get("kind") or "extension"
                    nested.append({"line": ln, "at": at, "name": name, "status": it.get("status"),
                                   "started_ms": started, "completed_ms": completed})
                elif ityp == "SubAgentActivity":
                    subagent_items.append({"line": ln, "at": at, "kind": it.get("kind"), "path": it.get("agent_path"),
                                           "id": it.get("id")})
                elif ityp == "ContextCompaction":
                    cc_items.append({"line": ln, "at": at, "turn": cur, "in_turn": cur is not None and cur["state"] == "open"})
            elif typ == "response_item" and sub == "message" and pl.get("role") in ("developer", "user"):
                text = _message_text(pl.get("content"))
                if "<skills_instructions>" in text:
                    skills_available.update(parse_skills_block(text.split("<skills_instructions>", 1)[1]))
            elif typ == "response_item" and sub in ("custom_tool_call", "function_call"):
                name = pl.get("name") or "unknown"
                ns = pl.get("namespace")
                inp = pl.get("input") if sub == "custom_tool_call" else pl.get("arguments")
                if not isinstance(inp, str):
                    inp = json.dumps(inp, sort_keys=True, ensure_ascii=False)
                kind = _call_kind(name, ns)
                full_args = {}
                if sub == "function_call":
                    try:
                        full_args = json.loads(inp)
                    except ValueError:
                        full_args = {}
                    if not isinstance(full_args, dict):
                        full_args = {}
                if kind == "exec":
                    cmds = extract_js_cmds(inp)
                elif kind == "shell":
                    raw_cmd = full_args.get("cmd") or full_args.get("command")
                    cmds = [shell_of(raw_cmd)] if raw_cmd else []
                else:
                    cmds = []
                c = {"line": ln, "at": at, "turn": cur, "call_id": pl.get("call_id"), "name": name, "namespace": ns,
                     "kind": kind, "server": ns[5:] if (ns or "").startswith("mcp__") else None,
                     "input_head": inp[:300], "args": _shrink(full_args),
                     "cmds": [x[:4000] for x in cmds[:30]], "n_cmds": len(cmds),
                     "patch_files": _PATCH_FILE.findall(inp)[:10],
                     "fns": list(dict.fromkeys(re.findall(r"tools\.(\w+)", inp)))[:10],
                     "hash": hashlib.sha256((name + "\0" + inp).encode()).hexdigest(),
                     "is_wait": kind in ("sleep", "poll") or name == "wait_agent" or (kind == "exec" and bool(_WAITY.search(inp))),
                     "has_patch": kind == "patch" or "apply_patch" in inp,
                     "cwd": full_args.get("workdir") if isinstance(full_args.get("workdir"), str) else None,
                     "out_line": None, "out_at": None, "out_head": "", "out_text": "", "wall_s": None, "dur_s": None,
                     "chunks": [], "proc_exit": None, "running": False, "cell": None, "timed_out": False,
                     "is_error_flag": False,
                     "ces": [], "mcp": [], "edits": [], "nested": [], "late_ces": [],
                     "state": "unknown", "note": None}
                calls.append(c)
                calls_by_id[c["call_id"]] = c
            elif typ == "response_item" and sub in ("custom_tool_call_output", "function_call_output"):
                c = calls_by_id.get(pl.get("call_id"))
                if c is not None:
                    parts = _output_parts(pl.get("output"))
                    head = parts[0] if parts else ""
                    text = _text_start(parts)
                    c["out_line"], c["out_at"] = ln, at
                    c["out_head"] = head.split("\n", 1)[0][:200]
                    c["out_text"] = text
                    m = _WALL.search(head[:400])
                    if m:
                        c["wall_s"] = float(m.group(1))
                    if head.startswith("Script "):
                        c["chunks"] = _inner_chunks(parts)
                        m = _CELL_RUNNING.match(head)
                        c["cell"] = m.group(1) if m else None
                    m = _PROC_EXIT.search(text[:600])
                    if m:
                        c["proc_exit"] = int(m.group(1))
                    c["running"] = bool(_PROC_RUNNING.search(text[:600]))
                    c["timed_out"] = bool(_TIMED_OUT.search(text))
                    c["is_error_flag"] = bool(_IS_ERROR.search(text))
                    parts = None

    if prev_total:
        seg_final.append(prev_total)
    final_sum = collections.Counter()
    for tot in seg_final:
        for k in _TOK_KEYS:
            final_sum[k] += tot.get(k) or 0
    tok_stat["tc_sum"] = {k: tc_sum[k] for k in _TOK_KEYS}
    tok_stat["final_sum"] = {k: final_sum[k] for k in _TOK_KEYS}

    # ContextCompaction items duplicate `compacted` records (a few lines apart): pair them one-to-one by
    # distance and keep only unpaired items as extra compactions
    pairs = sorted((abs(c["line"] - it["line"]), ci, ii) for ci, c in enumerate(compactions)
                   for ii, it in enumerate(cc_items) if abs(c["line"] - it["line"]) <= 12)
    used_c, used_i = set(), set()
    for _, ci, ii in pairs:
        if ci not in used_c and ii not in used_i:
            used_c.add(ci)
            used_i.add(ii)
    for ii, item in enumerate(cc_items):
        if ii not in used_i:
            compactions.append({"line": item["line"], "at": item["at"], "turn": item["turn"], "window": None,
                                "ctx_before": None, "in_turn": item["in_turn"], "from_item": True})
    compactions.sort(key=lambda c: c["line"])

    P = {"sid": None, "path": str(path), "sha256": sha.hexdigest(), "records": ln, "bad_lines": bad_lines,
         "line_dt": line_dt, "line_kind": line_kind, "meta": meta, "tctx": tctx, "models": models, "turns": turns,
         "prompts": prompts, "answers": answers, "aborts": aborts, "compactions": compactions, "calls": calls,
         "api": api, "tok_stat": tok_stat, "ces": ces, "mcp_items": mcp_items, "edits": edits, "nested": nested,
         "subagent_items": subagent_items, "skills_available": skills_available, "prices": prices,
         "mtime": path.stat().st_mtime}
    m = re.search(r"([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$", path.name)
    P["sid"] = meta.get("id") or (m.group(1) if m else path.stem)
    _attribute_items(P)
    _call_states(P)
    _pseudo_commands(P)
    _derive(P)
    return P


def _attribute_items(P):
    """Attach inner items (commands, MCP, edits, nested) to the top-level call that observed them."""
    calls = P["calls"]
    starts = [c["line"] for c in calls]
    by_ms = sorted(((ms_of(c["at"]), c) for c in calls if c["at"] is not None), key=lambda x: x[0])
    ms_keys = [m for m, _ in by_ms]
    by_id = {c["call_id"]: c for c in calls}

    def owner(item):
        i = bisect.bisect_right(starts, item["line"]) - 1
        if i >= 0:
            c = calls[i]
            end = c["out_line"] if c["out_line"] is not None else float("inf")
            if c["line"] < item["line"] < end:
                return c, False
        s = item.get("started_ms")
        if s is not None and ms_keys:
            j = bisect.bisect_right(ms_keys, s + 50) - 1
            if j >= 0:
                return by_ms[j][1], True
        return None, False

    for ce in P["ces"]:
        c, late = owner(ce)
        ce["call"] = c
        if c is not None:
            (c["late_ces"] if late else c["ces"]).append(ce)
            if not ce["cwd"] and c.get("cwd"):
                ce["cwd"] = c["cwd"]
    for m in P["mcp_items"]:
        c = by_id.get(m["id"])
        if c is not None and c["kind"] == "mcp":
            m["direct"] = True
        else:
            c, _ = owner(m)
        m["call"] = c
        if c is not None:
            c["mcp"].append(m)
    for e in P["edits"]:
        c, _ = owner(e)
        e["call"] = c
        if c is not None:
            c["edits"].append(e)
    for n in P["nested"]:
        c, _ = owner(n)
        n["call"] = c
        if c is not None:
            c["nested"].append(n)


def _pseudo_commands(P):
    """Old rollouts have no CommandExecution items: take shell commands from the call itself
    (``cmd:"…"`` in the exec script, or the exec_command arguments). The exit code is used only when it
    can be matched one-to-one; otherwise it stays unknown."""
    extra = []
    for c in P["calls"]:
        if not c["cmds"] or c["ces"] or c["late_ces"]:
            continue
        if c["kind"] == "shell":
            codes = [(c["proc_exit"], c["wall_s"])]
        else:
            chunks = [(code, wall) for code, wall, _ in c["chunks"]]
            codes = chunks if len(chunks) == c["n_cmds"] else [(None, None)] * c["n_cmds"]
        for k, cmd in enumerate(c["cmds"]):
            code, wall = codes[k] if k < len(codes) else (None, None)
            ce = {"line": c["line"], "at": c["at"], "turn": c["turn"], "started_ms": ms_of(c["at"]),
                  "completed_ms": None, "cmd": cmd, "cwd": c.get("cwd") or "", "exit": code, "status": None,
                  "dur_s": wall, "parsed": [], "stderr": "", "output": "", "pseudo": True, "call": c}
            ce.update(_cmd_facts(cmd, "", None, c.get("cwd")))
            extra.append(ce)
            c["ces"].append(ce)
    if extra:
        P["ces"] = sorted(P["ces"] + extra, key=lambda x: x["line"])
    P["pseudo_cmds"] = len(extra)


def _first_line(text, limit):
    for ln in (text or "").splitlines():
        if ln.strip():
            return clean(ln, limit)
    return None


_ERR_TIERS = [re.compile(r"(?i)(\w*error\b|\bfatal\b|\w*exception\b|\btraceback\b)"),
              re.compile(r"(?i)\b(failed|failure|not found|denied|no such|invalid|refused|timed? ?out)\b")]


def _error_line(text, limit):
    """Most telling line of an error output: a line naming an error/exception, then one saying
    failed/denied/not found, else the last non-empty line."""
    lines = [ln for ln in (text or "").splitlines() if ln.strip()]
    for rx in _ERR_TIERS:
        for ln in lines:
            if rx.search(ln):
                return clean(ln, limit)
    return clean(lines[-1], limit) if lines else None


def _script_detail(text):
    """Message after 'Script error:' / 'Output:' in a failed exec output."""
    for marker in ("Script error:", "Output:"):
        if marker in text:
            rest = text.split(marker, 1)[1]
            line = _first_line(rest, 130)
            if line:
                return line
    return _first_line(text, 130) or ""


def _call_states(P):
    for c in P["calls"]:
        head = c["out_head"]
        text = c["out_text"]
        k = c["kind"]
        if c["out_line"] is None:
            c["state"], c["note"] = "unknown", "нет результата в rollout"
        elif k in ("exec", "poll", "shell", "patch"):
            real_ces = [ce for ce in c["ces"] + c["late_ces"] if not ce["pseudo"]]
            failed_ces = [ce for ce in real_ces if (ce["exit"] not in (0, None)) or ce["status"] == "failed"]
            exits = [code for code, _, _ in c["chunks"] if code is not None]
            if c["proc_exit"] is not None:
                exits.append(c["proc_exit"])
            running = any(r for _, _, r in c["chunks"]) or c["running"] or c["cell"] is not None
            failed_mcp = [m for m in c["mcp"] if m["failed"]]
            failed_edits = [e for e in c["edits"] if e["status"] == "failed"]
            script_failed = head.startswith(("Script failed", "Script error", "Script timed out"))
            if failed_ces:
                ce = failed_ces[0]
                code = ce["exit"] if ce["exit"] is not None else "?"
                detail = _error_line(ce["stderr"], 110) or _error_line(ce["output"], 110) or ""
                late = " (фоновая, завершилась позже)" if ce in c["late_ces"] else ""
                c["state"], c["note"] = "error", clean(f"exit {code}{late} · {detail}", 160)
            elif script_failed:
                c["state"], c["note"] = "error", clean(f"{head.split(chr(10))[0][:20]} · {_script_detail(text)}", 160)
            elif c["timed_out"]:
                c["state"], c["note"] = "error", clean("таймаут выполнения · " + (_error_line(text, 120) or ""), 160)
            elif any(x != 0 for x in exits):
                code = next(x for x in exits if x != 0)
                detail = _error_line(text.split("Output:", 1)[-1], 110) if k in ("shell", "poll", "patch") else ""
                c["state"], c["note"] = "error", clean(f"exit {code}" + (f" · {detail}" if detail else ""), 160)
            elif failed_mcp:
                m = failed_mcp[0]
                c["state"], c["note"] = "error", clean(f"MCP {m['server']}.{m['tool']} · {_first_line(m['err'], 110) or 'failed'}", 160)
            elif failed_edits:
                c["state"], c["note"] = "error", "apply_patch · failed"
            elif real_ces or exits or c["mcp"] or c["edits"]:
                c["state"] = "unknown" if (running and not real_ces and not exits) else "success"
                if c["state"] == "unknown":
                    c["note"] = "процесс ещё выполнялся; код выхода не виден в этом вызове"
            elif c["cell"] is not None:
                c["state"], c["note"] = "unknown", f"скрипт продолжает работу (cell {c['cell']}); результат — в следующем wait"
            elif running:
                c["state"], c["note"] = "unknown", "процесс ещё выполнялся; код выхода не виден в этом вызове"
            else:
                c["state"], c["note"] = "unknown", "результат без кода выхода"
        elif k == "mcp":
            direct = [m for m in c["mcp"] if m["direct"]]
            if direct:
                m = direct[0]
                if m["failed"]:
                    c["state"], c["note"] = "error", clean(_first_line(m["err"], 150) or "MCP failed", 160)
                else:
                    c["state"] = "success"
            elif c["is_error_flag"]:
                c["state"], c["note"] = "error", "MCP isError"
            elif c["timed_out"]:
                c["state"], c["note"] = "error", clean(_error_line(text, 150) or "таймаут", 160)
            else:
                c["state"] = "unknown"
        else:
            if c["is_error_flag"] or re.search(r'"error"\s*:\s*(true|"[^"])', text) or re.match(
                    r"(?i)\s*(error\b|failed\b|invalid\b|unknown (process|agent|tool)\b|[^\n]{0,80}\bmust be\b)", text):
                c["state"], c["note"] = "error", clean(_first_line(text, 150), 160)
            else:
                c["state"] = "success"
        if c["wall_s"] is None and c["out_at"] is not None and c["at"] is not None:
            c["dur_s"] = (c["out_at"] - c["at"]).total_seconds()
        else:
            c["dur_s"] = c["wall_s"]


# --------------------------------------------------------------------------- derived metrics


def _day(d):
    return d.strftime("%Y-%m-%d")


def _split_by_day(a, b):
    """[(date, minutes)] for interval a..b split at UTC midnights."""
    out = []
    if a is None or b is None or b <= a:
        return out
    x = a
    while x < b:
        nxt = dt.datetime(x.year, x.month, x.day, tzinfo=dt.timezone.utc) + dt.timedelta(days=1)
        y = min(nxt, b)
        out.append((_day(x), (y - x).total_seconds() / 60.0))
        x = y
    return out


def _derive(P):
    line_dt = [d for d in P["line_dt"] if d is not None]
    act = [d for d, kind in zip(P["line_dt"], P["line_kind"]) if d is not None and kind not in _PASSIVE]
    P["start"] = line_dt[0] if line_dt else None
    P["end"] = act[-1] if act else (line_dt[-1] if line_dt else None)
    P["last_record"] = line_dt[-1] if line_dt else None
    last = P["end"]

    # waits: request_user_input* until the next human message
    prompts = P["prompts"]
    p_lines = [p["line"] for p in prompts]
    waits = []
    for c in P["calls"]:
        if c["kind"] != "wait":
            continue
        i = bisect.bisect_right(p_lines, c["line"])
        nxt = prompts[i] if i < len(prompts) else None
        qs = c["args"].get("questions") or []
        title = (qs[0].get("title") or qs[0].get("question")) if qs and isinstance(qs[0], dict) else None
        waits.append({"call": c, "line": c["line"], "at": c["at"], "reply": nxt,
                      "min": minutes(c["at"], nxt["at"]) if nxt else None, "title": title})
    P["waits"] = waits
    intervals = sorted((w["at"], w["reply"]["at"]) for w in waits if w["reply"] is not None)
    total, cur_a, cur_b = 0.0, None, None
    for a, b in intervals:
        if cur_b is None or a > cur_b:
            if cur_b is not None:
                total += minutes(cur_a, cur_b)
            cur_a, cur_b = a, b
        else:
            cur_b = max(cur_b, b)
    if cur_b is not None:
        total += minutes(cur_a, cur_b)
    P["wait_min"] = round(total, 1) if intervals else (None if waits else 0.0)

    # turns: an open turn (no task_complete/turn_aborted) ends at its last real event, never at the next
    # turn or at the end of the session
    for t in P["turns"]:
        if t["state"] == "open":
            end = t.get("last_at") or t["start"]
            t["end_eff"] = end
            t["dur_ms_eff"] = int((end - t["start"]).total_seconds() * 1000) if (end and t["start"]) else None
        else:
            t["dur_ms_eff"] = t["dur_ms"]
            t["end_eff"] = t["end"]

    # user_min: answer/abort/question/last event of an open turn -> your next message;
    # gaps > AWAY_MIN count as away (0)
    anchors = sorted([a["at"] for a in P["answers"]] + [a["at"] for a in P["aborts"]] + [w["at"] for w in waits]
                     + [t["end_eff"] for t in P["turns"] if t["state"] == "open" and t["end_eff"] is not None])
    user_gaps = []
    prev = None
    for p in prompts:
        lo = bisect.bisect_right(anchors, prev) if prev is not None else 0
        hi = bisect.bisect_left(anchors, p["at"])
        if hi > lo:
            gap = minutes(anchors[hi - 1], p["at"])
            p["gap_min"] = gap
            user_gaps.append((p, gap if gap <= AWAY_MIN else 0.0, gap > AWAY_MIN))
        else:
            p["gap_min"] = None
        prev = p["at"]
    P["user_gaps"] = user_gaps
    P["user_min"] = round(sum(g for _, g, _ in user_gaps), 1)
    P["away_gaps"] = sum(1 for *_, away in user_gaps if away)

    # skills
    avail_by_path = {os.path.normpath(p): n for n, p in P["skills_available"].items()}
    acts = []
    for ce in P["ces"]:
        for path in ce["skills"]:
            norm = os.path.normpath(path)
            name = avail_by_path.get(norm)
            if name is None:
                dirname = os.path.basename(os.path.dirname(norm))
                cands = [n for n, p in P["skills_available"].items() if os.path.basename(os.path.dirname(p)) == dirname]
                name = cands[0] if len(cands) == 1 else dirname
            acts.append({"line": ce["line"], "at": ce["at"], "turn": ce["turn"], "path": path, "name": name,
                         "source": skill_source(path), "exit": ce["exit"], "cmd": ce["cmd"]})
    P["skill_acts"] = acts

    # commits / PRs: only runs with a visible exit code 0 count
    commits = prs = 0
    stats, commit_ok, unconfirmed = [], 0, 0
    for ce in P["ces"]:
        if ce["exit"] != 0:
            if ce["exit"] is None and (ce["commit"] or ce["pr"]):
                unconfirmed += 1
            continue
        if ce["commit"]:
            commits += 1
            if ce["stat"]:
                commit_ok += 1
                stats.append(ce["stat"])
        if ce["pr"]:
            prs += 1
    P["commits"], P["prs"] = commits, prs
    P["commit_unconfirmed"] = unconfirmed
    if commits and commit_ok == commits:
        P["added"], P["removed"] = sum(a for a, _ in stats), sum(r for _, r in stats)
    else:
        P["added"] = P["removed"] = None
    P["commit_stat_missing"] = commits - commit_ok

    P["friction"] = _session_friction(P)


def newest(evidence, n):
    """Самые свежие n доказательств в порядке времени. Период находки считается по времени доказательств: старые
    эпизоды не должны вытеснять новые, когда окно данных длиннее периода.

    ``at`` — строка ``iso()`` (UTC, одна длина, сравнивается как текст) или None; без времени — как самые старые.
    При равном времени остаётся более поздний во входе (сортировка устойчива)."""
    if n <= 0:
        return []
    return sorted(evidence, key=lambda e: e.get("at") or "")[-n:]


def _session_friction(P):
    sid = P["sid"]
    out = {k: [] for k in FRICTION_KEYS}
    calls = P["calls"]

    def ev(line, at, text, cost=None):
        return {"sid": sid, "line": line, "at": iso(at), "text": clean(text, 200), "_cost": cost}

    # retry
    used = set()
    for i, c in enumerate(calls):
        if id(c) in used or c["is_wait"] or c["state"] != "error":
            continue
        same = [x for x in calls[i:i + RETRY_WINDOW] if x["hash"] == c["hash"]]
        if len(same) < RETRY_MIN or any(x["state"] != "error" or x["is_wait"] for x in same):
            continue
        lo, hi = same[0]["line"], same[-1]["line"]
        same_ids = {id(x) for x in same}
        windows = [(x["line"], x["out_line"] or x["line"]) for x in same]
        if any(lo < x["line"] < hi and id(x) not in same_ids and x["has_patch"] for x in calls) or any(
                lo < e["line"] < hi and not any(a <= e["line"] <= b for a, b in windows) for e in P["edits"]):
            continue
        used.update(id(x) for x in same)
        label = _call_title(c)
        out["retry"].append({"lines": [x["line"] for x in same], "evidence": [
            ev(x["line"], x["at"], f"повтор {k + 1}/{len(same)} · {label} · {x['note'] or 'ошибка'}")
            for k, x in enumerate(same)]})

    # thrash
    tests = [ce for ce in P["ces"] if ce["is_test"]]
    streak = []
    edit_ls = sorted(e["line"] for e in P["edits"])

    def flush():
        if len(streak) >= THRASH_MIN:
            out["thrash"].append({"lines": [x["line"] for x in streak], "evidence": [
                ev(x["line"], x["at"], f"провал {k + 1}/{len(streak)} · {x['norm']} · exit {x['exit']}")
                for k, x in enumerate(streak)]})

    for ce in tests:
        failed = ce["exit"] not in (0, None)
        if not failed:
            flush()
            streak = []
            continue
        if streak:
            a = streak[-1]["line"]
            has_edit = bisect.bisect_right(edit_ls, a) < len(edit_ls) and edit_ls[bisect.bisect_right(edit_ls, a)] < ce["line"]
            if not has_edit:
                flush()
                streak = []
        streak.append(ce)
    flush()

    # mcpfail
    by_server = collections.defaultdict(list)
    for m in P["mcp_items"]:
        if m["failed"]:
            by_server[m["server"]].append(m)
    for server, items in by_server.items():
        if len(items) >= MCPFAIL_MIN:
            out["mcpfail"].append({"lines": [m["line"] for m in items], "evidence": [
                ev(m["line"], m["at"], f"{server}.{m['tool']} · {_first_line(m['err'], 120) or 'failed'}") for m in items[-8:]]})

    # coldcache
    api = P["api"]
    api_lines = [a["line"] for a in api]
    for p in P["prompts"]:
        i = bisect.bisect_left(api_lines, p["line"])
        if i == 0 or i >= len(api):
            continue
        prev, first = api[i - 1], api[i]
        pause = minutes(prev["at"], p["at"])
        if pause is None or pause <= COLD_PAUSE_MIN or not first["input"]:
            continue
        ratio = first["cached"] / first["input"]
        if ratio >= COLD_RATIO:
            continue
        price = price_for(first["model"], P["prices"])
        extra = ((first["input"] - first["cached"]) * (price["input"] - price["cached"]) / 1e6) if price else None
        out["coldcache"].append({"lines": [first["line"]], "evidence": [ev(
            first["line"], first["at"],
            f"пауза {pause:.0f} мин → кэш {ratio * 100:.0f}% из {fmt_ktok(first['input'])} входа")], "cost": extra,
            "prompt_line": p["line"]})

    # compact
    for k, cp in enumerate(P["compactions"], 1):
        before = f" · контекст до сжатия {fmt_ktok(cp['ctx_before'])}" if cp["ctx_before"] else ""
        where = "посреди хода" if cp["in_turn"] else "между ходами"
        out["compact"].append({"lines": [cp["line"]], "evidence": [
            ev(cp["line"], cp["at"], f"сжатие №{cp['window'] or k} {where}{before}")]})

    # reread
    reads = collections.defaultdict(list)
    for ce in P["ces"]:
        seen = set()
        span = read_span(ce["cmd"])
        for item in ce["parsed"]:
            if isinstance(item, dict) and item.get("type") == "read" and item.get("path"):
                key = (item["path"], span)
                if key not in seen:
                    seen.add(key)
                    reads[key].append(ce)
    for (path, span), items in sorted(reads.items(), key=lambda kv: -len(kv[1])):
        if len(items) >= REREAD_MIN:
            name = os.path.basename(path) + ("" if span == "весь файл" else f" {span}")
            out["reread"].append({"lines": [x["line"] for x in items], "evidence": [
                ev(x["line"], x["at"], f"{name} · чтение {k + 1}/{len(items)}") for k, x in enumerate(items)][-8:],
                "file": clean(path, 160)})

    # wait
    for w in P["waits"]:
        tail = f"ждал {w['min']:.1f} мин" if w["min"] is not None else "ответа не было"
        out["wait"].append({"lines": [w["line"]], "evidence": [
            ev(w["line"], w["at"], f"{tail} · {w['title'] or 'вопрос агента'}")], "min": w["min"]})

    # abort
    for a in P["aborts"]:
        out["abort"].append({"lines": [a["line"]], "evidence": [
            ev(a["line"], a["at"], f"ход {a['turn']['turn'] if a['turn'] else '?'} прерван · {a['reason'] or 'причина не указана'}")]})
    return out


def _call_title(c, limit=140):
    """Short human title of a top-level call for the timeline (from fields precomputed at parse time)."""
    k = c["kind"]
    args = c["args"] or {}
    if k in ("exec", "shell"):
        cmds = list(c["cmds"])
        n = c["n_cmds"]
        if not cmds and (c["ces"] or c["late_ces"]):
            cmds = [ce["cmd"] for ce in c["ces"] + c["late_ces"]]
            n = len(cmds)
        if cmds:
            more = f" +{n - 1}" if n > 1 else ""
            return clean(cmds[0], limit - len(more)) + more
        if c["patch_files"]:
            more = f" +{len(c['patch_files']) - 1}" if len(c["patch_files"]) > 1 else ""
            return clean("apply_patch · " + os.path.basename(c["patch_files"][0]), limit - len(more)) + more
        fns = c["fns"]
        if fns == ["write_stdin"]:
            return "write_stdin · опрос процесса"
        if fns:
            return clean("exec · " + ", ".join(fns[:3]), limit)
        return clean("exec · " + (c["input_head"].strip().splitlines() or [""])[0], limit)
    if k == "patch":
        files = c["patch_files"]
        more = f" +{len(files) - 1}" if len(files) > 1 else ""
        return clean("apply_patch · " + (os.path.basename(files[0]) if files else ""), limit - len(more)) + more
    if k == "poll":
        if c["name"] == "write_stdin":
            chars = args.get("chars")
            return "write_stdin · " + ("ввод в процесс" if chars else "опрос процесса")
        cell = args.get("cell_id")
        return f"wait · ожидание скрипта (cell {cell})" if cell is not None else "wait · ожидание скрипта"
    if k == "mcp":
        direct = [m for m in c["mcp"] if m["direct"]]
        title = (direct[0]["title"] if direct else None) or args.get("title")
        code = args.get("code") or (direct[0]["code"] if direct else None)
        body = title or _first_line(code, limit) or ""
        return clean(f"{c['server']}.{c['name']} · {body}", limit)
    if k == "wait":
        qs = args.get("questions") or []
        q = qs[0] if qs and isinstance(qs[0], dict) else {}
        return clean("вопрос вам · " + (q.get("title") or q.get("question") or ""), limit)
    if k == "agent":
        target = args.get("task_name") or args.get("target") or args.get("agent_path") or ""
        return clean(f"{c['name']} → {target}" if target else c["name"], limit)
    if k == "sleep":
        ms = args.get("duration_ms")
        return f"sleep {ms / 1000:.0f} с" if isinstance(ms, (int, float)) else "sleep"
    return clean(c["name"], limit)


# --------------------------------------------------------------------------- outputs


def line_at(P, line):
    """ISO timestamp of the record at ``line`` (1-based) or the nearest record before it."""
    if line is None:
        return None
    try:
        line = int(line)
    except (TypeError, ValueError):
        return None
    if line < 1 or not P["line_dt"]:
        return None
    i = min(line, len(P["line_dt"])) - 1
    while i >= 0 and P["line_dt"][i] is None:
        i -= 1
    return iso(P["line_dt"][i]) if i >= 0 else None


_CHATGPT_PROJECT = re.compile(r"^g-p-[0-9a-f]{16,}$")
_MIRROR_NAME = re.compile(r"ChatGPT project [“\"«]([^”\"»]{1,60})[”\"»]")


def project_name(cwd):
    """Имя проекта — папка cwd. Для локальной копии ChatGPT-проекта (…/g-p-<hash>) —
    имя из её AGENTS.md («local mirror of the ChatGPT project “Пекарня”»), иначе сама папка."""
    if not cwd:
        return None
    base = os.path.basename(cwd.rstrip("/"))
    if _CHATGPT_PROJECT.match(base):
        try:
            with open(os.path.join(cwd, "AGENTS.md"), encoding="utf-8") as fh:
                m = _MIRROR_NAME.search(fh.read(4000))
            if m:
                return m.group(1).strip()
        except OSError:
            pass
    return base


def session_core(P):
    """Telemetry-derived Session fields of the contract."""
    tok = _new_tok()
    for a in P["api"]:
        for k in tok:
            tok[k] += a[k]
    costs = [a["cost"] for a in P["api"]]
    cost = None if (not costs or any(c is None for c in costs)) else round(sum(costs), 4)
    calls = P["calls"]
    cwd = P["meta"].get("cwd") or next((v["cwd"] for v in P["tctx"].values() if v.get("cwd")), None)
    model = P["models"].most_common(1)[0][0] if P["models"] else None
    first = next((p for p in P["prompts"] if p["kind"] == "prompt"), None)
    active = sum((t["dur_ms_eff"] or 0) for t in P["turns"]) / 60000.0
    fr = P["friction"]
    flags = [k for k in FRICTION_KEYS if fr.get(k)]
    return {
        "id": P["sid"], "short": short_id(P["sid"]), "agent": "codex", "source_path": P["path"],
        "source_sha256": P["sha256"], "records": P["records"],
        "project": project_name(cwd), "cwd": cwd, "branch": None,
        "start": iso(P["start"]), "end": iso(P["end"]),
        "wall_min": round(minutes(P["start"], P["end"]), 1) if P["start"] else None,
        "active_min": round(active, 1), "user_min": P["user_min"],
        "turns": len(P["turns"]), "prompts": len(P["prompts"]), "corrections": None,
        "reqs": len(P["api"]), "model": model, "tok": tok,
        "cache_hit": round(tok["cached"] / tok["input"], 3) if tok["input"] else None,
        "cost_usd": cost,
        "calls": len(calls), "errors": sum(c["state"] == "error" for c in calls),
        "unknown_results": sum(c["state"] == "unknown" for c in calls),
        "commits": P["commits"], "prs": P["prs"], "added": P["added"], "removed": P["removed"],
        "first": clean(first["text"], 200) if first else None,
        "title": clean(first["text"], 200) if first else None,
        "flags": flags, "compactions": len(P["compactions"]), "aborted": len(P["aborts"]),
        "waits": len(P["waits"]), "wait_min": P["wait_min"],
        "outcome": "unknown", "outcome_basis": None, "tasks": [], "checks": [], "deep": None,
        "sources": {"transcript": "available", "hooks": "missing", "otel": "missing", "deep": "none",
                    "coverage": False, "test_review": False},
        "daily": session_daily(P),
    }


def _turn_work_interval(t):
    """Interval that carries the turn's duration for daily splitting: the last ``dur_ms`` before the turn end.
    Old rollouts report duration_ms much shorter than started→completed, so the whole span would overcount."""
    a, b = t["start"], t["end_eff"]
    if a is None or b is None:
        return None, None
    if t["dur_ms_eff"] is not None:
        a = max(a, b - dt.timedelta(milliseconds=t["dur_ms_eff"]))
    return a, b


def session_daily(P):
    days = collections.OrderedDict()

    def d(day):
        if day not in days:
            days[day] = {"date": day, "cost_usd": 0.0, "reqs": 0, "tok": _new_tok(), "agent_min": 0.0,
                         "user_min": 0.0, "calls": 0, "errors": 0, "_cost_missing": False}
        return days[day]

    for a in P["api"]:
        if a["at"] is None:
            continue
        x = d(_day(a["at"]))
        x["reqs"] += 1
        for k in ("input", "cached", "output", "reasoning"):
            x["tok"][k] += a[k]
        if a["cost"] is None:
            x["_cost_missing"] = True
        else:
            x["cost_usd"] += a["cost"]
    for t in P["turns"]:
        for day, m in _split_by_day(*_turn_work_interval(t)):
            d(day)["agent_min"] += m
    for p, gap, _ in P["user_gaps"]:
        d(_day(p["at"]))["user_min"] += gap
    for c in P["calls"]:
        if c["at"] is None:
            continue
        x = d(_day(c["at"]))
        x["calls"] += 1
        x["errors"] += c["state"] == "error"
    out = []
    for day in sorted(days):
        x = days[day]
        x["cost_usd"] = None if x.pop("_cost_missing") else round(x["cost_usd"], 4)
        x["agent_min"] = round(x["agent_min"], 1)
        x["user_min"] = round(x["user_min"], 1)
        out.append(x)
    return out


def session_timeline(P):
    """Content of sessions/<id>.json."""
    sid = P["sid"]
    start = P["start"]
    events = []

    def tnum(t):
        return t["turn"] if t else 0

    def add(line, at, turn, k, x, note=None, side=None, tool=None):
        events.append({"line": line, "at": iso(at), "turn": tnum(turn), "k": k, "x": x, "note": note,
                       "side": side, "tool": tool})

    for p in P["prompts"]:
        add(p["line"], p["at"], p["turn"], "prompt", clean(p["text"], 240) or "—",
            "ответ на вопрос агента" if p["kind"] == "reply" else None, "вы")
    for a in P["answers"]:
        t = a["turn"]
        add(a["line"], a["at"], t, "answer", clean(a["text"], 240) or "—", None,
            fmt_dur((t["dur_ms"] or 0) / 1000) if t and t["dur_ms"] is not None else None)
    for a in P["aborts"]:
        t = a["turn"]
        add(a["line"], a["at"], t, "abort", f"ход прерван · {a['reason'] or 'без причины'}", None,
            fmt_dur((t["dur_ms"] or 0) / 1000) if t and t["dur_ms"] is not None else None)
    waits_by_call = {id(w["call"]): w for w in P["waits"]}
    for c in P["calls"]:
        title = _call_title(c)
        side = fmt_dur(c["dur_s"])
        if c["kind"] == "wait":
            w = waits_by_call.get(id(c))
            side = f"ждал {w['min']:.1f} мин" if w and w["min"] is not None else "без ответа"
            add(c["line"], c["at"], c["turn"], "wait", title, None, side, c["name"])
        elif c["kind"] == "agent":
            add(c["line"], c["at"], c["turn"], "agent", title, c["note"] if c["state"] == "error" else None, side, c["name"])
        else:
            k = "err" if c["state"] == "error" else "tool"
            note = c["note"] if c["state"] in ("error", "unknown") else None
            add(c["line"], c["at"], c["turn"], k, title, note, side, c["name"])
    for s in P["skill_acts"]:
        add(s["line"], s["at"], s["turn"], "skill", clean(f"skill · {s['name']}", 140),
            clean(s["path"], 160) + (f" · exit {s['exit']}" if s["exit"] not in (0, None) else ""), None, "exec")
    for cp in P["compactions"]:
        add(cp["line"], cp["at"], cp["turn"], "compact", f"сжатие контекста №{cp['window'] or '?'}", None,
            f"до сжатия {fmt_ktok(cp['ctx_before'])}" if cp["ctx_before"] else None)
    cum, ctx = [[0, 0]], []
    total = 0.0
    for a in P["api"]:
        ratio = (a["cached"] / a["input"] * 100) if a["input"] else 0
        add(a["line"], a["at"], a["turn"], "api",
            f"{a['model'] or 'модель?'} · вход {fmt_ktok(a['input'])} (кэш {ratio:.0f}%)",
            f"выход {a['output']} · рассуждения {a['reasoning']}" + (" · из token_count" if a["src"] == "token_count" else ""),
            fmt_usd(a["cost"]) if a["cost"] is not None else None)
        mins = round(minutes(start, a["at"]) or 0, 2)
        if a["cost"] is not None:
            total += a["cost"]
            cum.append([mins, round(total, 4)])
        ctx.append([mins, round(a["input"] / 1000, 1)])
    events.sort(key=lambda e: (e["line"], e["k"] != "tool"))
    tools = collections.Counter(c["name"] for c in P["calls"])
    turns = []
    for t in P["turns"]:
        turns.append({"turn": t["turn"], "turn_id": t["turn_id"], "start": iso(t["start"]), "end": iso(t["end"]),
                      "dur_ms": t["dur_ms"] if t["state"] != "open" else t["dur_ms_eff"], "state": t["state"],
                      "prompt_line": t["prompt_line"], "prompt": clean(t["prompt"], 200),
                      "answer": clean(t["answer"], 300),
                      "tok": {"input": t["tok"]["input"], "cached": t["tok"]["cached"], "output": t["tok"]["output"]}})
    return {"id": sid, "events": events, "cum": cum, "ctx": ctx, "tools": dict(tools.most_common()), "turns": turns}


# --------------------------------------------------------------------------- aggregates


def _mcp_unknown(m):
    """Исход вызова MCP неизвестен: нет ни ошибки, ни статуса завершения. Одно правило для «Инструментов» и «MCP»."""
    return not m["failed"] and m["status"] != "completed"


def aggregate_tools(parsed):
    rows = collections.OrderedDict()

    def row(key, name, kind, server):
        if key not in rows:
            rows[key] = {"name": name, "kind": kind, "server": server, "calls": 0, "errors": 0, "unknown": 0,
                         "p50_s": None, "p95_s": None, "by_session": {}, "_d": []}
        return rows[key]

    for P in parsed:
        sid = P["sid"]
        for c in P["calls"]:
            kind = "mcp" if c["kind"] == "mcp" else "builtin"
            r = row(("top", c["name"], c["server"]), c["name"], kind, c["server"])
            r["calls"] += 1
            r["errors"] += c["state"] == "error"
            r["unknown"] += c["state"] == "unknown"
            r["_d"].append(c["dur_s"])
            bs = r["by_session"].setdefault(sid, {"calls": 0, "errors": 0, "unknown": 0})
            bs["calls"] += 1
            bs["errors"] += c["state"] == "error"
            bs["unknown"] += c["state"] == "unknown"
        nested_items = []
        for ce in P["ces"]:
            if ce["pseudo"] and ce["call"] is not None and ce["call"]["kind"] == "shell":
                continue   # the direct exec_command call is already a top-level row
            err = (ce["exit"] not in (0, None)) or ce["status"] == "failed"
            nested_items.append(("exec_command", None, err, ce["exit"] is None and not err, ce["dur_s"]))
        for e in P["edits"]:
            nested_items.append(("apply_patch", None, e["status"] == "failed", e["status"] is None, None))
        for m in P["mcp_items"]:
            if not m["direct"]:
                nested_items.append((f"{m['server']}.{m['tool']}", m["server"], m["failed"], _mcp_unknown(m), m["dur_s"]))
        for n in P["nested"]:
            c = n.get("call")
            if c is None or c["kind"] != "exec":
                continue
            dur = ((n["completed_ms"] - n["started_ms"]) / 1000.0) if n["completed_ms"] and n["started_ms"] else None
            nested_items.append((n["name"], None, n["status"] == "failed", n["status"] is None, dur))
        for name, server, err, unk, dur in nested_items:
            r = row(("nested", name, server), name, "nested", server)
            r["calls"] += 1
            r["errors"] += bool(err)
            r["unknown"] += bool(unk and not err)
            r["_d"].append(dur)
            bs = r["by_session"].setdefault(sid, {"calls": 0, "errors": 0, "unknown": 0})
            bs["calls"] += 1
            bs["errors"] += bool(err)
            bs["unknown"] += bool(unk and not err)
    out = []
    for r in rows.values():
        d = r.pop("_d")
        r["p50_s"], r["p95_s"] = pct(d, 0.5), pct(d, 0.95)
        out.append(r)
    out.sort(key=lambda r: (r["kind"] == "nested", -r["calls"]))
    return out


def aggregate_mcp(parsed):
    rows = {}
    for P in parsed:
        for m in P["mcp_items"]:
            r = rows.setdefault(m["server"], {"server": m["server"], "calls": 0, "errors": 0, "unknown": 0, "avg_s": None,
                                              "direct": 0, "nested": 0, "by_session": {}, "_d": []})
            unk = _mcp_unknown(m)
            r["calls"] += 1
            r["errors"] += m["failed"]
            r["unknown"] += unk
            r["direct" if m["direct"] else "nested"] += 1
            r["_d"].append(m["dur_s"])
            bs = r["by_session"].setdefault(P["sid"], {"calls": 0, "errors": 0, "unknown": 0})
            bs["calls"] += 1
            bs["errors"] += m["failed"]
            bs["unknown"] += unk
    out = []
    for r in rows.values():
        d = [x for x in r.pop("_d") if x is not None]
        r["avg_s"] = round(sum(d) / len(d), 2) if d else None
        out.append(r)
    return sorted(out, key=lambda r: -r["calls"])


def aggregate_commands(parsed, sessions):
    rows = {}
    proj = {s["id"]: s["project"] for s in sessions}
    for P in parsed:
        for ce in P["ces"]:
            key = (ce["norm"], proj.get(P["sid"]))
            r = rows.setdefault(key, {"cmd": key[0], "project": key[1], "runs": 0, "failed": 0, "unknown": 0,
                                      "p95_s": None, "total_min": 0.0, "by_session": {}, "_d": []})
            unk = ce["exit"] is None and ce["status"] != "failed"
            failed = (ce["exit"] not in (0, None)) or ce["status"] == "failed"
            r["runs"] += 1
            r["failed"] += failed
            r["unknown"] += unk
            r["_d"].append(ce["dur_s"])
            bs = r["by_session"].setdefault(P["sid"], {"runs": 0, "failed": 0, "unknown": 0})
            bs["runs"] += 1
            bs["failed"] += failed
            bs["unknown"] += unk
    out = []
    for r in rows.values():
        d = r.pop("_d")
        r["p95_s"] = pct(d, 0.95)
        r["total_min"] = round(sum(x for x in d if x is not None) / 60.0, 2)
        out.append(r)
    return sorted(out, key=lambda r: (-r["runs"], r["cmd"]))


_POLICY_LABEL = {"never": "политика never · без запроса", "on-request": "политика on-request · агент может запросить",
                 "on-failure": "политика on-failure · запрос при сбое", "untrusted": "политика untrusted · запрос на непроверенное"}
_SANDBOX_LABEL = {"danger-full-access": "песочница выключена (danger-full-access)",
                  "workspace-write": "песочница workspace-write", "read-only": "песочница read-only"}


def aggregate_permissions(parsed):
    rows = collections.OrderedDict()

    def bump(label, sid, n=1):
        r = rows.setdefault(label, {"label": label, "count": 0, "by_session": {}})
        r["count"] += n
        r["by_session"][sid] = r["by_session"].get(sid, 0) + n

    for P in parsed:
        sid = P["sid"]
        for c in P["calls"]:
            ctxt = P["tctx"].get(c["turn"]["turn_id"]) if c["turn"] else None
            pol = (ctxt or {}).get("approval") or "неизвестна"
            bump(_POLICY_LABEL.get(pol, f"политика {pol}"), sid)
        for c in P["calls"]:
            ctxt = P["tctx"].get(c["turn"]["turn_id"]) if c["turn"] else None
            sb = (ctxt or {}).get("sandbox") or "неизвестна"
            bump(_SANDBOX_LABEL.get(sb, f"песочница {sb}"), sid)
        n_wait = sum(1 for c in P["calls"] if c["kind"] == "wait")
        bump("агент спросил вас", sid, n_wait)
    return list(rows.values())


def correction_items(P, classes):
    """Friction occurrences for turns classed as 'correction' ({turn_index: class})."""
    out = []
    by_idx = {t["turn"]: t for t in P["turns"]}
    for idx in sorted(classes):
        if classes[idx] != "correction":
            continue
        t = by_idx.get(idx)
        if t is None:
            continue
        line = t["prompt_line"] or t["start_line"]
        out.append({"lines": [line], "evidence": [{"sid": P["sid"], "line": line, "at": line_at(P, line),
                                                    "text": clean(f"ход {idx}: «{t['prompt'] or ''}»", 200), "_cost": None}]})
    return out


def aggregate_friction(parsed, classes_by_sid=None):
    classes_by_sid = classes_by_sid or {}
    out = []
    for key, name, sev, how in FRICTION_META:
        occ = []
        known = True
        for P in parsed:
            if key == "correction":
                cls = classes_by_sid.get(P["sid"])
                items = correction_items(P, cls) if cls else []
            else:
                items = P["friction"].get(key, [])
            occ.extend((P["sid"], it) for it in items)
        if key == "correction" and not any(classes_by_sid.get(P["sid"]) for P in parsed):
            known = False
        costs = [it.get("cost") for _, it in occ if it.get("cost") is not None]
        evidence = []
        for sid, it in occ:
            for e in it["evidence"]:
                evidence.append({k: v for k, v in e.items() if k != "_cost"})
        out.append({"key": key, "name": name, "how": how, "sev": sev,
                    "sessions": sorted({sid for sid, _ in occ}),
                    "by_session": dict(collections.Counter(sid for sid, _ in occ)),
                    "count": len(occ) if known else None,
                    "cost_usd": round(sum(costs), 4) if (key == "coldcache" and occ and len(costs) == len(occ)) else None,
                    "evidence": newest(evidence, 60)})
    return out


def _merge_segments(items, gap_s):
    segs = []
    for a, b, label in sorted(items, key=lambda x: x[0]):
        if segs and (a - segs[-1][1]).total_seconds() < gap_s:
            segs[-1][1] = max(segs[-1][1], b)
            segs[-1][2].append(label)
        else:
            segs.append([a, b, [label]])
    return segs


def aggregate_skills(parsed, classes_by_sid=None):
    classes_by_sid = classes_by_sid or {}
    available = {}
    for P in parsed:
        available.update(P["skills_available"])
    rows = collections.OrderedDict()
    for P in parsed:
        cls = classes_by_sid.get(P["sid"]) or {}
        turns = P["turns"]
        edit_items = sorted([(e["at"], e["line"]) for e in P["edits"] if e["at"] is not None])
        for s in P["skill_acts"]:
            r = rows.setdefault(s["name"], {"name": s["name"], "source": s["source"], "activations": 0,
                                            "sessions": [], "by_session": {}, "first_line": None, "to_code_min": None,
                                            "corrections_after": 0, "subagents": 0,
                                            "size_ktok": None, "state": "used", "evidence": [], "_to_code": []})
            r["activations"] += 1
            r["by_session"][P["sid"]] = r["by_session"].get(P["sid"], 0) + 1
            if P["sid"] not in r["sessions"]:
                r["sessions"].append(P["sid"])
            if r["first_line"] is None:
                r["first_line"] = s["line"]
            try:
                r["size_ktok"] = round(os.path.getsize(s["path"]) / 4 / 1000, 1)
            except OSError:
                pass
            t = s["turn"]
            if t is not None:
                end = t["end_eff"]
                nxt_edit = next(((at, ln) for at, ln in edit_items if ln > s["line"] and (end is None or at <= end)), None)
                if nxt_edit:
                    r["_to_code"].append(minutes(s["at"], nxt_edit[0]))
                if cls.get(t["turn"] + 1) == "correction":
                    r["corrections_after"] += 1
                r["subagents"] += sum(1 for c in P["calls"] if c["kind"] == "agent" and c["turn"] is t
                                      and c["line"] > s["line"] and c["name"] in ("spawn_agent", "followup_task"))
            text = clean(s["cmd"], 140) + (f" · exit {s['exit']}" if s["exit"] not in (0, None) else "")
            r["evidence"].append({"sid": P["sid"], "line": s["line"], "at": iso(s["at"]), "text": text})
    out = []
    for r in rows.values():
        tc = [x for x in r.pop("_to_code") if x is not None]
        r["to_code_min"] = round(min(tc), 1) if tc else None
        out.append(r)
    unused = len([n for n in available if n not in rows])
    if unused:
        out.append({"name": "…", "source": f"остальные {unused} skills — 0 активаций", "activations": 0, "sessions": [],
                    "by_session": {}, "first_line": None, "to_code_min": None, "corrections_after": 0, "subagents": 0,
                    "size_ktok": None, "state": "unused", "evidence": []})
    return {"available": len(available) if available else None, "rows": out, "gantt": _gantt(parsed)}


def _gantt(parsed):
    best = None
    for P in parsed:
        score = collections.Counter()
        for s in P["skill_acts"]:
            score[_day(s["at"])] += 1
        for c in P["calls"]:
            if c["kind"] == "agent" and c["name"] in ("spawn_agent", "followup_task", "send_message"):
                score[_day(c["at"])] += 1
        for day, n in score.items():
            if best is None or n > best[0]:
                best = (n, P, day)
    if best is None:
        return None
    _, P, day = best
    act_turns = set()
    for s in P["skill_acts"]:
        if _day(s["at"]) == day and s["turn"] is not None:
            act_turns.add(s["turn"]["turn"])
    for c in P["calls"]:
        if c["kind"] == "agent" and _day(c["at"]) == day and c["turn"] is not None:
            act_turns.add(c["turn"]["turn"])
    tlist = [t for t in P["turns"] if t["turn"] in act_turns]
    if not tlist:
        return None
    lo = min(t["start"] for t in tlist)
    hi = max(t["end_eff"] or t["start"] for t in tlist)
    inside = lambda d: d is not None and lo <= d <= hi  # noqa: E731
    rows = []
    skill_segs = collections.OrderedDict()
    for s in P["skill_acts"]:
        if inside(s["at"]) and s["turn"] is not None:
            end = s["turn"]["end_eff"] or hi
            skill_segs.setdefault(s["name"], []).append(
                {"from": iso(s["at"]), "to": iso(min(end, hi)), "c": "skill", "label": clean(s["name"], 60), "_a": s["at"], "_b": min(end, hi)})
    for name, segs in skill_segs.items():
        rows.append({"name": clean(name, 60), "segs": [{k: v for k, v in g.items() if not k.startswith("_")} for g in segs]})
    sub = []
    completions = [x for x in P["subagent_items"] if x["kind"] == "completed"]
    for c in P["calls"]:
        if c["kind"] == "agent" and inside(c["at"]):
            done = next((x["at"] for x in completions if x["at"] > c["at"]), None)
            end = done or (c["turn"]["end_eff"] if c["turn"] else None) or c["at"]
            sub.append({"from": iso(c["at"]), "to": iso(min(end, hi)), "c": "subagent", "label": clean(_call_title(c), 60)})
    if sub:
        rows.append({"name": "субагенты", "segs": sub})
    mitems = [(m["at"] - dt.timedelta(seconds=m["dur_s"] or 0), m["at"], m["server"]) for m in P["mcp_items"] if inside(m["at"])]
    if mitems:
        segs = _merge_segments(mitems, MCP_MERGE_S)
        rows.append({"name": "MCP", "segs": [
            {"from": iso(a), "to": iso(b), "c": "mcp",
             "label": f"{len(ls)} выз. · " + ", ".join(sorted(set(str(x) for x in ls)))} for a, b, ls in segs]})
    noskill = []
    all_skill = sorted((g["_a"], g["_b"]) for segs in skill_segs.values() for g in segs)
    for t in P["turns"]:
        a, b = t["start"], t["end_eff"]
        if a is None or b is None or b < lo or a > hi:
            continue
        a, b = max(a, lo), min(b, hi)
        x = a
        for sa, sb in all_skill:
            if sb <= x or sa >= b:
                continue
            if sa > x:
                noskill.append((x, sa))
            x = max(x, sb)
        if x < b:
            noskill.append((x, b))
    if noskill:
        rows.append({"name": "без skills", "segs": [
            {"from": iso(a), "to": iso(b), "c": "noskill", "label": "ход без skill"} for a, b in noskill
            if (b - a).total_seconds() >= 1]})
    marks = [{"at": iso(p["at"]), "text": "«" + (clean(p["text"], 28) or "") + "»", "line": p["line"]}
             for p in P["prompts"] if inside(p["at"])]
    project = project_name(P["meta"].get("cwd")) or "?"
    return {"sid": P["sid"], "title": f"{short_id(P['sid'])} · {day} · {project}", "from": iso(lo), "to": iso(hi),
            "rows": rows, "marks": marks}


def session_gaps(P):
    """Per-session honest notes for dataset.gaps."""
    g = []
    short = short_id(P["sid"])
    if P["last_record"] and P["end"] and P["last_record"] > P["end"]:
        g.append(f"{short}: последние записи rollout ({iso(P['last_record'])[:10]}) — служебные (настройки/контекст), "
                 f"не работа; end и wall_min считаются по последнему событию работы ({iso(P['end'])}).")
    if P["bad_lines"]:
        g.append(f"{short}: {len(P['bad_lines'])} строк rollout не разобраны (вероятно, файл ещё дописывается); "
                 f"первая — L{P['bad_lines'][0]}.")
    open_turns = [t for t in P["turns"] if t["state"] == "open"]
    if open_turns:
        which = ", ".join(f"{t['turn']} (L{t['start_line']})" for t in open_turns[:5])
        g.append(f"{short}: {len(open_turns)} ход(а) без task_complete/turn_aborted — {which}; их длительность — до последнего "
                 f"события внутри хода, а не до следующего хода или конца файла.")
    gapped = [t for t in P["turns"] if t["state"] != "open" and t["start"] and t["end"] and t["dur_ms"] is not None
              and (t["end"] - t["start"]).total_seconds() * 1000 - t["dur_ms"] > 30 * 60 * 1000]
    if gapped:
        extra_h = sum((t["end"] - t["start"]).total_seconds() / 3600 - t["dur_ms"] / 3600000 for t in gapped)
        g.append(f"{short}: у {len(gapped)} ход(ов) промежуток task_started → task_complete длиннее duration_ms больше чем "
                 f"на 30 мин (в сумме {extra_h:.1f} ч без записей работы); active_min берёт duration_ms как есть, "
                 f"в daily длительность ставится в конец хода.")
    long_turns = [t for t in P["turns"] if (t["dur_ms_eff"] or 0) > LONG_TURN_H * 3600 * 1000]
    if long_turns:
        worst = max(long_turns, key=lambda t: t["dur_ms_eff"])
        g.append(f"{short}: {len(long_turns)} ход(а) длиннее {LONG_TURN_H} ч (самый долгий — ход {worst['turn']}, L{worst['start_line']}, "
                 f"{worst['dur_ms_eff'] / 3600000:.1f} ч, {worst['state']}); длительность взята как есть, не обрезана.")
    ts = P["tok_stat"]
    if ts["tc_used"]:
        match = ts["tc_sum"] == ts["final_sum"]
        resets = f" с учётом {ts['resets']} сброс(ов) счётчика" if ts["resets"] else ""
        part = "всех" if not ts["tur"] else f"{ts['tc_used']} из {len(P['api'])}"
        g.append(f"{short}: токены {part} ответов модели взяты из token_count (в этой части rollout нет token_usage_record; "
                 f"повторные опросы с тем же total_token_usage отброшены: {ts['tc_dup']}); сумма token_count "
                 f"{'совпадает' if match else 'НЕ совпадает'} с итоговым total_token_usage{resets}.")
    if P.get("pseudo_cmds"):
        known = sum(1 for ce in P["ces"] if ce["pseudo"] and ce["exit"] is not None)
        g.append(f"{short}: {P['pseudo_cmds']} команд взяты из текста exec-скриптов/аргументов (нет записей CommandExecution); "
                 f"код выхода виден у {known}, у остальных failed не считается (commands.unknown).")
    if P.get("commit_unconfirmed"):
        g.append(f"{short}: {P['commit_unconfirmed']} команд git commit / gh pr create без видимого кода выхода — "
                 f"в commits/prs не входят.")
    if P["commits"] and P["added"] is None:
        g.append(f"{short}: у {P['commit_stat_missing']} из {P['commits']} коммитов в выводе нет строки «files changed» — "
                 f"added/removed = null.")
    unknown = sum(c["state"] == "unknown" for c in P["calls"])
    if unknown:
        g.append(f"{short}: результат {unknown} вызовов не определён (нет кода выхода: фоновый процесс, опрос, "
                 f"вызов без внутренних команд).")
    if P["waits"] and any(w["reply"] is None for w in P["waits"]):
        n = sum(w["reply"] is None for w in P["waits"])
        g.append(f"{short}: на {n} вопрос(ов) агента нет следующей реплики — в wait_min не входят.")
    if P["away_gaps"]:
        g.append(f"{short}: {P['away_gaps']} пауз(ы) перед вашей репликой длиннее {AWAY_MIN:.0f} мин считаются отсутствием "
                 f"и в user_min не входят.")
    return g


GENERAL_GAPS = [
    "user_min — сумма пауз от ответа агента (task_complete, turn_aborted, вопрос request_user_input или последнее событие "
    "хода без task_complete) до вашей следующей реплики; паузы > 30 мин считаются отсутствием и дают 0; реплики посреди хода "
    "без предшествующего ответа не учитываются.",
    "active_min — сумма длительностей ходов (duration_ms из task_complete/turn_aborted); ход без завершения длится до своего "
    "последнего события. Ходы длиннее 3 ч не обрезаются и перечислены ниже.",
    "cost_usd — оценка по одной условной цене API для всех моделей Codex: (вход − кэш) × 1.25 + кэш × 0.125 + выход × 10 $ "
    "за 1 млн; токены рассуждений уже входят в выход (total = вход + выход). Фактически — подписка Codex.",
    "Токены: основной источник — token_usage_record (по одному на ответ модели, включая запросы сжатия контекста); "
    "в старых rollout его нет, там берётся event_msg token_count → last_token_usage, повторные опросы с тем же "
    "total_token_usage отбрасываются, token_count с тем же расходом, что и соседний token_usage_record, не дублируется.",
    "Ошибка вызова: код выхода ≠ 0 у любой внутренней команды exec (в том числе rg/grep без совпадений — exit 1), "
    "«Script failed»/«Script error», «js execution timed out», статус failed/isError у MCP; если кода нет (старые "
    "exec-скрипты печатают только вывод, «Script running with cell ID») — вызов в unknown_results.",
    "calls — вызовы инструментов верхнего уровня (exec, wait/write_stdin, exec_command, apply_patch, MCP, request_user_input, "
    "субагенты, sleep). Команды, MCP-вызовы и правки внутри exec идут в tools[] с kind=nested и в commands[].",
    "write_stdin внутри exec-скрипта (опрос фоновых процессов) не оставляет отдельной записи; виден только как вызов exec.",
    "branch = null: в rollout нет надёжного поля ветки (в одной сессии бывает несколько репозиториев).",
    "Hooks и OTel этим сборщиком не читаются: sources.hooks/otel = missing.",
    "daily: ответы модели, вызовы и реплики — по дню своего события (UTC); ход через полночь делится между днями по времени.",
    "Skills: активация — команда чтения SKILL.md (cat/sed/head/tail/nl/rg/grep по пути …/SKILL.md, либо parsed_cmd read). "
    "to_code_min — от активации до первой правки файла в том же ходе. state всегда used: признаков noisy/slow/conflict "
    "без модели не выделить.",
    "reread считается по parsed_cmd Codex (type=read) с диапазоном строк из команды (sed -n a,bp, head/tail -n); чтение одного места после сжатия контекста тоже считается повтором.",
]
