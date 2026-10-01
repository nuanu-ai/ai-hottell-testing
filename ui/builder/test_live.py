"""Tests for live.py on synthetic ClickHouse JSONEachRow rows (invented content; no ClickHouse needed).

Run: cd ui/builder && python3 -m unittest -v
"""

import datetime as dt
import hashlib
import io
import json
import os
import re
import shutil
import stat
import sys
import tempfile
import unittest
import urllib.error
from pathlib import Path
from unittest import mock

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import live as L  # noqa: E402

FIX = HERE / "testdata" / "live"
A = "019cbd6f-b2a0-7aaa-8aaa-aaaaaaaaaaaa"      # Codex, started under hooks
P = "019cb2b6-0800-7bbb-8bbb-bbbbbbbbbbbb"      # Codex, created two days before its first hook event
C = "11111111-2222-4333-8444-555555555555"      # Claude Code
E = "019cbd39-ae80-7ccc-8ccc-cccccccccccc"      # SessionStart only -> dropped


def load(name):
    return [json.loads(ln) for ln in (FIX / f"{name}.jsonl").read_text().splitlines() if ln.strip()]


class FakeClickHouse:
    """query(name, sql) -> rows from the fixtures; hook rows are filtered by the time window in the SQL."""

    def __init__(self):
        self.rows = {n: load(n) for n in ("hooks", "claude_logs", "claude_metrics", "codex_otel", "codex_sse")}
        self.calls = []

    def __call__(self, name, sql):
        self.calls.append((name, sql))
        if name == "bounds":
            ts = sorted(r["ts"] for r in self.rows["hooks"])
            return [{"lo": ts[0], "hi": ts[-1], "n": len(ts)}]
        rows = self.rows[name]
        if name == "hooks":
            bounds = re.findall(r"Timestamp (>=|<) parseDateTime64BestEffort\('([^']+)'", sql)
            for op, value in bounds:
                limit = L.parse_ch_ts(value)
                rows = [r for r in rows if (L.parse_ch_ts(r["ts"]) >= limit if op == ">=" else L.parse_ch_ts(r["ts"]) < limit)]
        return rows


def build():
    raw = L.fetch(FakeClickHouse())
    return L.build_dataset(raw)


def _hook(ts, ev, sid, **kw):
    row = {k: "" for k in ("turn_id", "prompt_id", "tool", "tuid", "tin", "tin_hash", "resp_head", "resp_tail", "dur", "pm",
                            "atype", "aid", "cwd", "model", "mcp_server", "prompt", "last_msg", "title", "source", "reason",
                            "trig", "snapshot", "henrich", "hx", "hstatus", "hdur", "t_resp", "t_in", "t_cached", "t_out",
                            "t_reas", "t_model", "t_dur")}
    row.update(ts=ts, ev=f"agent.hook.{ev}", agent="codex", sid=sid, resp_len=0, cwd="/work/bakery", model="gpt-6-sol")
    row.update(kw)
    return row


T0 = dt.datetime(2026, 3, 8, 10, 0, tzinfo=dt.timezone.utc)


def _sid(t0, c):
    """uuid7 со временем t0: сессия «создана» в момент первого события, поэтому не partial."""
    ms = f"{int(t0.timestamp() * 1000):012x}"
    return f"{ms[:8]}-{ms[8:12]}-7{c * 3}-8{c * 3}-{c * 12}"


def _ts(t0, s):
    """Время ClickHouse; s может быть дробным (10.05 -> …:10.050000000)."""
    return (t0 + dt.timedelta(seconds=s)).strftime("%Y-%m-%d %H:%M:%S.%f") + "000"


def _call(t0, sid, s, tuid, tin, resp="ok", tool="Bash", post=True, resp_hash=None, **kw):
    """Пара PreToolUse/PostToolUse; post=False — только PreToolUse. resp_hash по умолчанию — хэш resp."""
    rows = [_hook(_ts(t0, s), "PreToolUse", sid, tool=tool, tuid=tuid, tin=tin, **kw)]
    if post:
        rows.append(_hook(_ts(t0, s + 1), "PostToolUse", sid, tool=tool, tuid=tuid, tin=tin, resp_head=resp,
                          resp_len=len(resp), resp_hash=resp_hash or hashlib.sha1(resp.encode()).hexdigest()[:16], **kw))
    return rows


def run(hooks, sse=(), now=None, codex_otel=()):
    fake = FakeClickHouse()
    fake.rows = {"hooks": hooks, "claude_logs": [], "claude_metrics": [], "codex_otel": list(codex_otel),
                 "codex_sse": list(sse)}
    return L.build_dataset(L.fetch(fake), now=now)


def enriched_dataset():
    """Codex session whose hook events the hottell drainer enriched from the session rollout."""
    import datetime as dt
    t0 = dt.datetime(2026, 3, 6, 10, 0, tzinfo=dt.timezone.utc)
    ms = f"{int(t0.timestamp() * 1000):012x}"
    sid = f"{ms[:8]}-{ms[8:12]}-7ddd-8ddd-dddddddddddd"
    ts = lambda s: (t0 + dt.timedelta(seconds=s)).strftime("%Y-%m-%d %H:%M:%S.000000000")
    hooks = [
        _hook(ts(1), "SessionStart", sid, source="startup"),
        _hook(ts(2), "UserPromptSubmit", sid, turn_id="T1", prompt="Посчитай заказ пекарни"),
        _hook(ts(3), "PreToolUse", sid, turn_id="T1", tool="Bash", tuid="exec-1", tin='{"command":"pytest -q"}'),
        _hook(ts(9), "PostToolUse", sid, turn_id="T1", tool="Bash", tuid="exec-1", resp_head="2 failed", resp_len=8,
              henrich="found", hx="2", hstatus="failed", hdur="5400"),
        _hook(ts(10), "PreToolUse", sid, turn_id="T1", tool="Bash", tuid="exec-2", tin='{"command":"ls"}'),
        _hook(ts(11), "PostToolUse", sid, turn_id="T1", tool="Bash", tuid="exec-2", resp_head="a b", resp_len=3,
              henrich="found", hstatus="completed"),
        _hook(ts(12), "PreToolUse", sid, turn_id="T1", tool="Bash", tuid="exec-3", tin='{"command":"cat x"}'),
        _hook(ts(13), "PostToolUse", sid, turn_id="T1", tool="Bash", tuid="exec-3", resp_head="x", resp_len=1,
              henrich="not_found"),
        _hook(ts(20), "Stop", sid, turn_id="T1", last_msg="Готово", henrich="found", t_resp="3", t_in="1000",
              t_cached="800", t_out="50", t_reas="10", t_model="gpt-6-sol", t_dur="18000"),
    ]
    fake = FakeClickHouse()
    fake.rows = {"hooks": hooks, "claude_logs": [], "claude_metrics": [], "codex_otel": [], "codex_sse": []}
    dataset, timelines = L.build_dataset(L.fetch(fake))
    return sid, dataset, timelines


class Sessions(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.ds, cls.tl = build()
        cls.s = {s["id"]: s for s in cls.ds["sessions"]}

    def test_grouping_and_drops(self):
        self.assertEqual(set(self.s), {A, P, C})             # install, hottell-*-check-*, empty session dropped
        self.assertEqual(self.ds["variant"], "live")
        self.assertEqual(self.ds["title"], "Новые сессии · Hooks + OTel")
        gaps = " ".join(self.ds["gaps"])
        self.assertIn("проверки hottell: 1", gaps)
        self.assertIn("019cbd39", gaps)
        self.assertIn("1 session.id без событий хуков", gaps)

    def test_codex_counts(self):
        s = self.s[A]
        self.assertEqual((s["prompts"], s["turns"], s["calls"], s["errors"], s["unknown_results"]), (3, 2, 12, 4, 1))
        self.assertEqual((s["commits"], s["prs"], s["added"], s["removed"]), (1, 0, 2, 1))
        # вопрос в 10:00:14 — не остановка: за секунду до него начался «sleep 100» без PostToolUse, простой не доказан
        self.assertEqual((s["compactions"], s["waits"], s["wait_min"]), (1, 1, 0.0))
        self.assertEqual(s["flags"], ["mcpfail", "compact"])
        self.assertEqual(s["model"], "gpt-demo")
        self.assertEqual(s["project"], "live-demo")
        self.assertEqual(s["sources"]["hooks"], "recorded")
        self.assertEqual(s["sources"]["transcript"], "not_used")
        # tokens only through codex.sse_event linked by conversation.id, priced with the assumed price
        self.assertEqual(s["sources"]["otel"], "linked")
        self.assertEqual(s["tok"], {"input": 3000, "cached": 1500, "output": 30, "reasoning": 5})
        self.assertAlmostEqual(s["cost_usd"], ((3000 - 1500) * 1.25 + 1500 * 0.125 + 30 * 10) / 1e6, places=4)
        self.assertEqual(self.ds["pricing"]["models"], {"gpt-demo": {"input": 1.25, "cached": 0.125, "output": 10.0}})

    def test_active_and_user_time(self):
        s = self.s[A]
        # turn t1 10:00:01 -> Stop 10:05:00; open turn t2 10:15:00 -> its last event 10:20:31 (not the session end)
        self.assertEqual(s["active_min"], 10.5)
        # answer 10:05:00 -> prompt 10:15:00 (10 min); the question at 10:00:14 is not a proven stop (see test_codex_counts)
        self.assertEqual(s["user_min"], 10.0)
        turns = self.tl[A]["turns"]
        self.assertEqual([t["state"] for t in turns], ["task_complete", "open"])
        self.assertEqual(turns[1]["dur_ms"], 331000)
        self.assertIsNone(turns[1]["end"])

    def test_partial_session(self):
        s = self.s[P]
        self.assertEqual(s["sources"]["hooks"], "partial")
        self.assertEqual((s["calls"], s["unknown_results"], s["prompts"]), (5, 5, 0))
        self.assertIsNone(s["tok"])
        self.assertIsNone(s["cost_usd"])
        self.assertEqual(s["sources"]["otel"], "unlinked")
        self.assertTrue(any(g.startswith("019cb2b6…bbbb: записан только хвост с 2026-03-05 11:00 UTC") for g in self.ds["gaps"]))

    def test_claude_session(self):
        s = self.s[C]
        self.assertEqual(s["agent"], "claude")
        self.assertEqual(s["title"], "Demo title")
        self.assertEqual((s["prompts"], s["turns"], s["calls"], s["errors"], s["unknown_results"]), (2, 2, 5, 1, 0))
        self.assertEqual(s["reqs"], 3)
        self.assertEqual(s["tok"], {"input": 3000, "cached": 1000, "output": 170, "reasoning": None})   # input + cache_read + cache_creation
        self.assertAlmostEqual(s["cost_usd"], 0.06)
        self.assertEqual(s["model"], "claude-demo")
        self.assertEqual(s["active_min"], 10.0)            # claude_code.active_time.total (cli)
        self.assertEqual(s["user_min"], 8.0)
        self.assertEqual((s["commits"], s["added"], s["removed"]), (1, 10, 2))
        self.assertEqual(s["flags"], ["coldcache"])
        self.assertEqual(s["sources"]["otel"], "recorded")
        self.assertEqual(s["daily"][0]["agent_min"], 10.0)

    def test_timeline(self):
        ev = self.tl[A]["events"]
        self.assertEqual([e["line"] for e in ev], list(range(1, len(ev) + 1)))
        self.assertTrue(all(e["at"] and e["k"] for e in ev))
        kinds = [e["k"] for e in ev]
        self.assertTrue({"prompt", "api", "tool", "err", "wait", "skill", "compact", "answer", "agent"} <= set(kinds))
        self.assertEqual(ev[0]["x"], "Fix the build please. password=[скрыто]")
        reply = next(e for e in ev if e["note"] == "ответ на вопрос агента")
        self.assertEqual(reply["x"], "ответ на вопрос агента: blue")
        patch = next(e for e in ev if e["tool"] == "apply_patch")
        self.assertEqual((patch["k"], patch["x"]), ("err", "apply_patch · a.py"))
        self.assertTrue(patch["note"].startswith("exit 1"))
        spawn = next(e for e in ev if e["tool"] == "collaboration.spawn_agent")
        self.assertEqual(spawn["k"], "agent")
        sub_call = next(e for e in ev if e["x"] == "ls")
        self.assertIn("субагент default", sub_call["note"])
        self.assertEqual(self.tl[A]["tools"]["Bash"], 6)
        self.assertEqual(self.tl[C]["cum"][-1][1], 0.06)
        self.assertNotIn("hunter2secret", json.dumps(self.tl, ensure_ascii=False) + json.dumps(self.ds, ensure_ascii=False))

    def test_fixture_sessions_are_user_work(self):
        self.assertEqual({s["kind"] for s in self.ds["sessions"]}, {"user"})


class Aggregates(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.ds, cls.tl = build()

    def test_tools_mcp_permissions(self):
        tools = {t["name"]: t for t in self.ds["tools"]}
        self.assertEqual(tools["Bash"]["by_session"][A], {"calls": 6, "errors": 0, "unknown": 1})
        self.assertEqual(tools["mcp__demo__run"]["kind"], "mcp")
        mcp = {m["server"]: m for m in self.ds["mcp"]}
        self.assertEqual((mcp["demo"]["calls"], mcp["demo"]["errors"]), (3, 3))
        self.assertIn("Claude Browser", mcp)                # parsed from the JSON mcp_server attribute
        perms = {p["label"]: p for p in self.ds["permissions"]}
        self.assertEqual(perms["Claude · разрешено настройками"]["count"], 5)
        self.assertEqual(perms["Claude · отклонено настройками"]["count"], 1)
        self.assertEqual(perms["Codex · режим bypassPermissions"]["by_session"][P], 5)
        cmds = {(c["cmd"], c["project"]): c for c in self.ds["commands"]}
        self.assertEqual(cmds[("git commit", "live-demo")]["by_session"][A], {"runs": 1, "failed": 0, "unknown": 1})

    def test_by_session_carries_unknown(self):
        # сумма по сессиям равна итогу строки; вызовы без результата сходятся с unknown_results сессии
        for key, fields in (("tools", ("calls", "errors", "unknown")), ("mcp", ("calls", "errors", "unknown")),
                            ("commands", ("runs", "failed", "unknown"))):
            for r in self.ds[key]:
                for f in fields:
                    self.assertEqual(sum(bs[f] for bs in r["by_session"].values()), r[f], (key, f))
        for s in self.ds["sessions"]:
            self.assertEqual(sum(r["by_session"].get(s["id"], {}).get("unknown", 0) for r in self.ds["tools"]),
                             s["unknown_results"])
        self.assertEqual(next(s["unknown_results"] for s in self.ds["sessions"] if s["id"] == A), 1)

    def test_claude_mcp_without_post_is_unknown(self):
        sid = "22222222-3333-4444-8555-666666666666"
        kw = dict(agent="claude", tool="mcp__srv__search", prompt_id="p1")
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, agent="claude", source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, agent="claude", prompt_id="p1", prompt="Найди задачи"),
                 *_call(T0, sid, 5, "m1", "{}", **kw), *_call(T0, sid, 10, "m2", "{}", post=False, **kw)]
        ds, _ = run(hooks)
        m = next(r for r in ds["mcp"] if r["server"] == "srv")
        t = next(r for r in ds["tools"] if r["name"] == "mcp__srv__search")
        for r in (m, t):
            self.assertEqual((r["calls"], r["errors"], r["unknown"]), (2, 0, 1))
            self.assertEqual(r["by_session"][sid], {"calls": 2, "errors": 0, "unknown": 1})

    def test_skills(self):
        sk = self.ds["skills"]
        self.assertEqual(sk["available"], 5)        # снимок {demo, other, third} ∪ открытые {demo, guide, demo-skill}
        self.assertGreaterEqual(sk["available"], len([r for r in sk["rows"] if r["state"] != "unused"]))
        names = {r["name"]: r for r in sk["rows"]}
        self.assertEqual(names["demo"]["activations"], 1)          # cat …/SKILL.md in Codex Bash
        self.assertEqual(names["guide"]["activations"], 1)         # Claude Read of SKILL.md
        self.assertEqual(names["demo-skill"]["source"], "Claude Skill")
        # активации по сессиям: интерфейс пересчитывает их под фильтр
        self.assertEqual((names["demo"]["by_session"], names["guide"]["by_session"]), ({A: 1}, {C: 1}))
        for r in sk["rows"]:
            self.assertEqual(sum(r["by_session"].values()), r["activations"])
        self.assertEqual(sk["rows"][-1]["state"], "unused")
        self.assertIsNotNone(sk["gantt"])

    def test_friction(self):
        fr = {f["key"]: f for f in self.ds["friction"]}
        self.assertEqual(fr["mcpfail"]["count"], 1)
        self.assertEqual(fr["coldcache"]["count"], 1)
        self.assertAlmostEqual(fr["coldcache"]["cost_usd"], 0.03)
        self.assertIsNone(fr["correction"]["count"])
        self.assertEqual(fr["retry"]["count"], 0)               # three MCP errors differ in input
        self.assertEqual(fr["mcpfail"]["by_session"], {A: 1})
        self.assertEqual(fr["coldcache"]["by_session"], {C: 1})
        self.assertEqual(fr["retry"]["by_session"], {})

    def test_findings(self):
        f = {x["title"]: x for x in self.ds["findings"]}
        self.assertEqual(set(f), {"Связать нативный OTel Codex с сессиями", "Получать код выхода Bash в хуке PostToolUse Codex"})
        self.assertTrue(all(x["scope"] == "collection" and x["source"] == "detector" for x in f.values()))
        otel = f["Связать нативный OTel Codex с сессиями"]
        self.assertIn("codex.api_request — 50", otel["what"])
        self.assertEqual(otel["sessions"], [P])
        self.assertIn("У остальных 1 токены", otel["what"])
        self.assertEqual(otel["impact"]["value"], f"{len(otel['sessions'])} сессий")
        self.assertEqual(otel["impact_by_session"], {P: 1})
        bash = f["Получать код выхода Bash в хуке PostToolUse Codex"]
        self.assertIn("У 5 из 5 результатов Bash", bash["what"])


class Helpers(unittest.TestCase):
    def test_uuid7_and_names(self):
        self.assertEqual(L.uuid7_time(A).strftime("%Y-%m-%d %H:%M"), "2026-03-05 09:59")
        self.assertIsNone(L.uuid7_time(C))
        self.assertEqual(L.display_tool("collaborationspawn_agent"), "collaboration.spawn_agent")
        self.assertEqual(L.display_tool("clockcurr_time"), "clock.curr_time")
        self.assertEqual(L.display_tool("webrun"), "web.run")
        self.assertEqual(L.display_tool("Bash"), "Bash")
        self.assertEqual(L.mcp_server_of("mcp__codex_apps__github__search_prs"), "codex_apps")
        self.assertEqual(L.mcp_server_of("mcp__x__y", '{"name":"Claude Browser","source":"sdk"}'), "Claude Browser")
        self.assertEqual(L.parse_ch_ts("2026-09-30 02:56:58.448339000").isoformat(), "2026-09-30T02:56:58.448339+00:00")

    def test_call_state(self):
        base = {"agent": "codex", "post_at": 1, "resp_len": 10, "cc_success": None}
        self.assertEqual(L.call_state(dict(base, resp_head="Exit code: 0\nWall time: 0 seconds"))[0], "success")
        self.assertEqual(L.call_state(dict(base, resp_head="Chunk ID: ab\nWall time: 0.1 seconds\nProcess exited with code 2\nOutput:\nx"))[0], "error")
        self.assertEqual(L.call_state(dict(base, resp_head="plain output"))[0], "done")
        self.assertEqual(L.call_state(dict(base, resp_head='{"error":"no such agent"}'))[0], "error")
        self.assertEqual(L.call_state(dict(base, resp_head='{"message":"Wait timed out.","timed_out":true}'))[0], "success")
        self.assertEqual(L.call_state(dict(base, resp_head="x" * 20, resp_tail='…"isError":false}', resp_len=90000))[0], "success")
        self.assertEqual(L.call_state(dict(base, post_at=None))[0], "unknown")
        self.assertEqual(L.call_state(dict(base, agent="claude", resp_head='{"stdout":"","interrupted":true}'))[0], "error")

    def test_read_targets(self):
        self.assertEqual(L.read_targets("sed -n '10,40p' a.py", "/w"), [("/w/a.py", "строки 10–40")])
        self.assertEqual(L.read_targets("cat /x/README.md | head -5"), [("/x/README.md", "первые 5")])
        self.assertEqual(L.read_targets("rg -n foo ."), [])

    def test_skill_name(self):
        self.assertEqual(L.skill_name("/nope/analytics/skill/SKILL.md"), "analytics")
        self.assertEqual(L.skill_name("/nope/playwright/SKILL.md"), "playwright")
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp, "skill", "SKILL.md")
            p.parent.mkdir()
            p.write_text("---\nname: hottell-analytics\ndescription: разбор\n---\n# x\n", encoding="utf-8")
            self.assertEqual(L.skill_name(str(p)), "hottell-analytics")
            body = Path(tmp, "cfgskill", "SKILL.md")      # name: в теле (пример YAML) — не имя skill
            body.parent.mkdir()
            body.write_text("---\ndescription: пример настроек\n---\n```yaml\nname: example-config\n```\n", encoding="utf-8")
            self.assertEqual(L.skill_name(str(body)), "cfgskill")

    def test_since_validation(self):
        with self.assertRaises(ValueError):
            L._time_cond("Timestamp", "2026-01-01'; DROP TABLE x; --")
        self.assertIn("parseDateTime64BestEffort('2026-09-30T00:00:00Z', 9)", L._time_cond("Timestamp", "2026-09-30T00:00:00Z"))


class QueryLayer(unittest.TestCase):
    def test_windowed_fetch_does_not_duplicate(self):
        fake = FakeClickHouse()
        raw = L.fetch(fake, window_h=1)
        self.assertGreater(sum(1 for n, _ in fake.calls if n == "hooks"), 1)
        self.assertEqual(len(raw["hooks"]), len(fake.rows["hooks"]))

    def test_parse_rows_detects_midstream_exception(self):
        rows, err = L.parse_rows('{"a":1}\n{"exception":"Code: 241. MEMORY_LIMIT_EXCEEDED"}\n')
        self.assertEqual(rows, [{"a": 1}])
        self.assertIn("MEMORY_LIMIT_EXCEEDED", err)

    def test_client_retries_on_memory_limit(self):
        responses = [io.BytesIO(b'{"exception":"Code: 241. DB::Exception: MEMORY_LIMIT_EXCEEDED"}\n'), io.BytesIO(b'{"n":5}\n')]

        class Resp:
            def __init__(self, buf):
                self.buf = buf

            def read(self):
                return self.buf.read()

            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False

        with mock.patch("urllib.request.urlopen", side_effect=lambda *a, **k: Resp(responses.pop(0))), \
                mock.patch("time.sleep"):
            self.assertEqual(L.ClickHouse()("x", "SELECT 1"), [{"n": 5}])

    def test_client_reports_other_errors(self):
        err = urllib.error.HTTPError("http://x", 400, "bad", {}, io.BytesIO(b"Code: 62. Syntax error"))
        with mock.patch("urllib.request.urlopen", side_effect=err):
            with self.assertRaises(L.ClickHouseError):
                L.ClickHouse()("x", "SELECT")


class Output(unittest.TestCase):
    def test_main_writes_private_files(self):
        tmp = Path(tempfile.mkdtemp())
        try:
            out = tmp / "ui-live"
            (out / "sessions").mkdir(parents=True)
            stale = out / "sessions" / "00000000-0000-4000-8000-000000000000.json"
            stale.write_text("{}")
            self.assertEqual(L.main(["--out", str(out)], query=FakeClickHouse()), 0)
            self.assertEqual(stat.S_IMODE(os.stat(out).st_mode), 0o700)
            self.assertEqual(stat.S_IMODE(os.stat(out / "sessions").st_mode), 0o700)
            files = [out / "dataset.json"] + sorted((out / "sessions").iterdir())
            self.assertEqual(len(files), 4)                  # dataset + 3 sessions; stale file removed
            self.assertFalse(stale.exists())
            for f in files:
                self.assertEqual(stat.S_IMODE(os.stat(f).st_mode), 0o600)
                json.loads(f.read_text())
        finally:
            shutil.rmtree(tmp)

    def test_until_is_build_time(self):
        # фиксированное окно воспроизводимо: «сейчас» для льгот и открытых ходов — конец окна, а не часы машины
        with tempfile.TemporaryDirectory() as tmp, mock.patch("sys.stdout", new_callable=io.StringIO):
            out = Path(tmp, "ui-live")
            self.assertEqual(L.main(["--out", str(out), "--until", "2026-03-06T00:00:00Z",
                                     "--reports-dataset", str(Path(tmp, "none.json"))], query=FakeClickHouse()), 0)
            ds = json.loads((out / "dataset.json").read_text())
        self.assertEqual(ds["generated_at"], "2026-03-06T00:00:00.000Z")


class TranscriptEnrichmentTest(unittest.TestCase):
    """Поля, которые дрейнер hottell добавил из журнала сессии Codex."""

    @classmethod
    def setUpClass(cls):
        cls.sid, cls.dataset, cls.timelines = enriched_dataset()
        cls.s = next(x for x in cls.dataset["sessions"] if x["id"] == cls.sid)

    def test_exit_code_and_status(self):
        self.assertEqual(self.s["calls"], 3)
        self.assertEqual(self.s["errors"], 1)                 # exit 2 из журнала, хотя текст ответа без кода
        self.assertEqual(self.s["unknown_results"], 0)
        err = next(e for e in self.timelines[self.sid]["events"] if e["k"] == "err")
        self.assertIn("exit 2 · из журнала сессии", err["note"])

    def test_outcome_known(self):
        # exit 2 (ошибка) и completed (успех) известны; «x» без кода выхода — исход неизвестен
        self.assertEqual(self.s["outcome_known"], 2)

    def test_turn_tokens_from_stop(self):
        self.assertEqual(self.s["tok"], {"input": 1000, "cached": 800, "output": 50, "reasoning": 10})
        self.assertEqual(self.s["reqs"], 3)
        self.assertEqual(self.s["cost_basis"], "api_price_estimate")
        self.assertIsNotNone(self.s["cost_usd"])
        self.assertEqual(self.s["sources"]["transcript_facts"], "recorded")
        self.assertIn("gpt-6-sol", self.dataset["pricing"]["models"])
        api = next(e for e in self.timelines[self.sid]["events"] if e["k"] == "api")
        self.assertIn("3 отв. за ход · из журнала сессии", api["note"])


class FullHistoryTest(unittest.TestCase):
    def test_marks_sessions_present_in_reports(self):
        dataset = {"sessions": [{"id": A}, {"id": P}]}
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp, "dataset.json")
            path.write_text(json.dumps({"sessions": [{"id": P, "title": "не читается"}]}))
            L.mark_full_history(dataset, path)
        self.assertEqual([s["full_history"] for s in dataset["sessions"]], [False, True])
        self.assertEqual(dataset["also_in_reports"], [P])
        L.mark_full_history(dataset, Path("/nonexistent/dataset.json"))
        self.assertEqual(dataset["also_in_reports"], [])


class TokenSourcesTest(unittest.TestCase):
    """Итог хода из журнала (Stop) и ответы codex.sse_event не складываются."""

    def test_rollout_turn_total_wins_over_otel_responses(self):
        import datetime as dt
        t0 = dt.datetime(2026, 3, 7, 10, 0, tzinfo=dt.timezone.utc)
        ms = f"{int(t0.timestamp() * 1000):012x}"
        sid = f"{ms[:8]}-{ms[8:12]}-7eee-8eee-eeeeeeeeeeee"
        ts = lambda s: (t0 + dt.timedelta(seconds=s)).strftime("%Y-%m-%d %H:%M:%S.000000000")
        hooks = [
            _hook(ts(1), "SessionStart", sid, source="startup"),
            _hook(ts(2), "UserPromptSubmit", sid, turn_id="T1", prompt="первый ход"),
            _hook(ts(20), "Stop", sid, turn_id="T1", henrich="found", t_resp="2", t_in="1000", t_cached="900",
                  t_out="40", t_reas="0", t_model="gpt-6-sol"),
            _hook(ts(30), "UserPromptSubmit", sid, turn_id="T2", prompt="второй ход"),
            _hook(ts(50), "Stop", sid, turn_id="T2", henrich="not_found"),
        ]
        sse = [{"ts": ts(10), "sid": sid, "kind": "response.completed", "model": "gpt-6-sol",
                "inp": "600", "cached": "500", "outp": "20", "reas": "0"},
               {"ts": ts(40), "sid": sid, "kind": "response.completed", "model": "gpt-6-sol",
                "inp": "300", "cached": "200", "outp": "10", "reas": "0"}]
        fake = FakeClickHouse()
        fake.rows = {"hooks": hooks, "claude_logs": [], "claude_metrics": [], "codex_otel": [], "codex_sse": sse}
        dataset, _ = L.build_dataset(L.fetch(fake))
        s = next(x for x in dataset["sessions"] if x["id"] == sid)
        # T1: только итог из журнала (1000), ответ OTel внутри T1 отброшен; T2: ответ OTel (300).
        self.assertEqual(s["tok"]["input"], 1300)
        self.assertEqual(s["reqs"], 3)


class SessionKindTest(unittest.TestCase):
    def test_only_explicit_service_sessions_are_system(self):
        t = [T0 + dt.timedelta(hours=h) for h in range(6)]
        sug, mem, hb, short, silent, mwa = (_sid(t[i], c) for i, c in enumerate("abcdef"))
        bash = '{"command":"git status"}'
        memdir = "/Users/x/.codex/memories"
        hooks = [
            _hook(_ts(t[0], 0), "SessionStart", sug, source="startup"),
            _hook(_ts(t[0], 1), "UserPromptSubmit", sug, turn_id="T1",
                  prompt="# Overview\nGenerate 0 to 3 hyperpersonalized suggestions for what this user can do"),
            *_call(t[0], sug, 5, "s1", bash),
            _hook(_ts(t[1], 0), "UserPromptSubmit", mem, turn_id="T1", prompt="consolidate", cwd=memdir),
            *_call(t[1], mem, 5, "m1", bash, cwd=memdir),
            _hook(_ts(t[2], 0), "UserPromptSubmit", hb, turn_id="T1", prompt="<heartbeat>\n<instructions>Разбери транскрипты"),
            *_call(t[2], hb, 5, "h1", bash),
            # контрпримеры из ревью: обычный короткий запрос и сессия без реплик остаются видимыми
            _hook(_ts(t[3], 0), "SessionStart", short, source="startup"),
            _hook(_ts(t[3], 1), "UserPromptSubmit", short, turn_id="T1", prompt="Проверь статус сервиса"),
            *_call(t[3], short, 5, "c1", '{"command":"systemctl status api"}'), *_call(t[3], short, 20, "c2", bash),
            *_call(t[4], silent, 0, "q1", bash), *_call(t[4], silent, 120, "q2", bash),
            # правило по реплике памяти — отдельно от правила по папке
            _hook(_ts(t[5], 0), "UserPromptSubmit", mwa, turn_id="T1",
                  prompt="## Memory Writing Agent: Phase 2 (Consolidation)\nYou are a Memory Writing Agent."),
            *_call(t[5], mwa, 5, "w1", bash),
        ]
        ds, _ = run(hooks)
        kinds = {s["id"]: s["kind"] for s in ds["sessions"]}
        self.assertEqual(kinds, {sug: "system", mem: "system", hb: "automation", short: "user", silent: "user",
                                 mwa: "system"})
        reasons = {s["id"]: s["kind_reason"] for s in ds["sessions"]}
        self.assertEqual(reasons[sug], "подсказки Codex Desktop")
        self.assertIsNone(reasons[short])

    def test_codex_ambient_suggestions_are_system(self):
        sid = _sid(T0, "f")
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1",
                       prompt="You are an expert at upholding safety and compliance standards for Codex ambient suggestions. Review"),
                 *_call(T0, sid, 5, "a1", '{"command":"git status"}')]
        ds, _ = run(hooks)
        s = next(x for x in ds["sessions"] if x["id"] == sid)
        self.assertEqual((s["kind"], s["kind_reason"]), ("system", "проверка подсказок Codex"))


class PartialTest(unittest.TestCase):
    """partial — запись началась не с начала сессии. Codex Desktop и Claude Code присылают
    SessionStart source=resume и при возврате к сессии, записанной с первого события."""

    def _session(self, sid, t0, sources):
        hooks = [_hook(_ts(t0, 0), "SessionStart", sid, source=sources[0]),
                 _hook(_ts(t0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Почини сборку"),
                 *_call(t0, sid, 5, "c1", '{"command":"make"}')]
        for k, src in enumerate(sources[1:], 1):
            s = 600 * k
            hooks += [_hook(_ts(t0, s), "SessionStart", sid, source=src),
                      _hook(_ts(t0, s + 1), "UserPromptSubmit", sid, turn_id=f"T{k + 1}", prompt="Продолжай"),
                      *_call(t0, sid, s + 5, f"c{k + 1}", '{"command":"make test"}')]
        ds, _ = run(hooks)
        return next(x for x in ds["sessions"] if x["id"] == sid)

    def test_resume_after_startup_is_recorded(self):
        s = self._session(_sid(T0, "a"), T0, ["startup", "resume", "resume"])
        self.assertEqual(s["sources"]["hooks"], "recorded")

    def test_resume_without_startup_is_partial(self):
        s = self._session(_sid(T0, "b"), T0, ["resume"])
        self.assertEqual(s["sources"]["hooks"], "partial")

    def test_late_first_hook_is_partial_even_with_startup(self):
        # uuid7: сессия создана за 2 ч до первого события хуков — проверка PARTIAL_AFTER_H остаётся
        s = self._session(_sid(T0 - dt.timedelta(hours=2), "c"), T0, ["startup"])
        self.assertEqual(s["sources"]["hooks"], "partial")

    def test_clear_starts_recording_compact_does_not(self):
        # /clear в Claude Code начинает новую сессию с первого события; compact — середина сессии
        s = self._session(_sid(T0, "d"), T0, ["clear", "resume"])
        self.assertEqual(s["sources"]["hooks"], "recorded")
        s = self._session(_sid(T0, "e"), T0, ["compact", "resume"])
        self.assertEqual(s["sources"]["hooks"], "partial")
        s = self._session(_sid(T0, "f"), T0, ["clear", "startup", "resume"])
        self.assertEqual(s["sources"]["hooks"], "recorded")


class ShortIdTest(unittest.TestCase):
    def test_short_id_format(self):
        self.assertEqual(L.T.short_id("01a0f57b-0000-7000-8000-000000004b74"), "01a0f57b…4b74")
        self.assertEqual(L.T.short_id("abc"), "abc")

    def test_sessions_started_in_one_minute_differ(self):
        # у uuid7 первые 8 знаков — время с шагом 65,536 с: две сессии в одном шаге
        base_ms = (int(T0.timestamp() * 1000) // 65536) * 65536 + 1000
        t1 = dt.datetime.fromtimestamp(base_ms / 1000, tz=dt.timezone.utc)
        t2 = t1 + dt.timedelta(seconds=30)
        a, b = _sid(t1, "a"), _sid(t2, "b")
        self.assertEqual(a[:8], b[:8])
        hooks = []
        for sid, t in ((a, t1), (b, t2)):
            hooks += [_hook(_ts(t, 0), "SessionStart", sid, source="startup"),
                      _hook(_ts(t, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Проверь сборку"),
                      *_call(t, sid, 5, "c-" + sid[-4:], '{"command":"make"}')]
        ds, _ = run(hooks)
        shorts = {s["id"]: s["short"] for s in ds["sessions"]}
        self.assertEqual(shorts[a], f"{a[:8]}…{a[-4:]}")
        self.assertNotEqual(shorts[a], shorts[b])


class RereadTest(unittest.TestCase):
    def test_only_same_agent_same_known_content_without_cuts(self):
        sid = _sid(T0, "1")
        cat = lambda f: json.dumps({"command": f"cat {f}"})
        patch = json.dumps({"command": "*** Begin Patch\n*** Update File: docs/b.md\n@@\n-x\n+y\n*** End Patch"})
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Разберись в документах")]
        step = iter(range(10, 100000, 10))

        def c(tin, **kw):
            kw.setdefault("resp", "same")
            hooks.extend(_call(T0, sid, next(step), f"t{len(hooks)}", tin, turn_id="T1", **kw))

        for _ in range(3):
            c(cat("docs/a.md"))                                         # единственный эпизод
        c(cat("docs/b.md")); c(cat("docs/b.md")); c(patch, tool="apply_patch", resp="Success"); c(cat("docs/b.md"))
        c(cat("docs/c.md")); c(cat("docs/c.md"))
        hooks.append(_hook(_ts(T0, next(step)), "PostCompact", sid, turn_id="T1", trig="auto"))
        c(cat("docs/c.md"))
        for k in range(3):
            c(cat("docs/d.md"), aid=f"sub-{k}", atype="default")        # каждый субагент — по разу
        c(cat("docs/d.md"))
        for _ in range(4):
            c(json.dumps({"command": "tail -n 27 ci.log"}))
        for k in range(3):
            c(cat("docs/e.md"), resp=f"версия {k}")                     # файл менялся
        for k in range(3):
            c(cat("docs/f.md"), resp_hash=f"h{k}")                      # контрпример: начало то же, дальше разное
        for _ in range(3):
            c(cat("docs/g.md"), post=False)                             # контрпример: результата нет
        for _ in range(3):
            c(cat("docs/h.md docs/i.md"))                               # ответ на два файла не разделить
        ds, _ = run(hooks)
        rr = next(f for f in ds["friction"] if f["key"] == "reread")
        self.assertEqual(rr["count"], 1)
        self.assertTrue(all("a.md" in e["text"] for e in rr["evidence"]))

    def test_claude_read_cut_by_edit(self):
        hooks = [_hook(_ts(T0, 0), "SessionStart", C, agent="claude", source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", C, agent="claude", prompt_id="p1", prompt="Разберись в документах")]
        step = iter(range(10, 10000, 10))

        def c(tool, args):
            hooks.extend(_call(T0, C, next(step), f"t{len(hooks)}", json.dumps(args), tool=tool, resp="same",
                               agent="claude", prompt_id="p1"))

        c("Read", {"file_path": "/work/x.md"}); c("Read", {"file_path": "/work/x.md"})
        c("Edit", {"file_path": "/work/x.md", "old_string": "a", "new_string": "b"})
        c("Read", {"file_path": "/work/x.md"})                         # правка между 2-м и 3-м чтением — эпизода нет
        for _ in range(3):
            c("Read", {"file_path": "/work/y.md"})                     # без правки — один эпизод
        ds, _ = run(hooks)
        rr = next(f for f in ds["friction"] if f["key"] == "reread")
        self.assertEqual(rr["count"], 1)
        self.assertTrue(all("y.md" in e["text"] for e in rr["evidence"]))

    def test_empty_recorded_response_is_unknown_content(self):
        sid = _sid(T0, "f")
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Разберись в документах")]
        for k in range(3):                                              # PostToolUse есть, resp_len=0
            hooks += _call(T0, sid, 10 + k * 10, f"z{k}", json.dumps({"command": "cat docs/a.md"}), resp="", turn_id="T1")
        ds, _ = run(hooks)
        rr = next(f for f in ds["friction"] if f["key"] == "reread")
        self.assertEqual(rr["count"], 0)

    def _three_reads(self, c, resp):
        sid = _sid(T0, c)
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Разберись в документах")]
        for k in range(3):
            hooks += _call(T0, sid, 10 + k * 10, f"r{k}", json.dumps({"command": "cat README.md"}), resp=resp, turn_id="T1")
        return next(f for f in run(hooks)[0]["friction"] if f["key"] == "reread")

    def test_failed_reads_are_not_rereading(self):
        # одинаковый текст ошибки даёт одинаковый хэш, но содержимого файла агент не видел
        rr = self._three_reads("a", "Exit code: 1\nWall time: 0 seconds\nOutput:\ncat: README.md: Permission denied")
        self.assertEqual(rr["count"], 0)
        rr = self._three_reads("c", "Exit code: 1\nWall time: 0 seconds\nOutput:\nREADME.md: input/output error")
        self.assertEqual(rr["count"], 0)                                # ошибку видно только по коду выхода

    def test_read_error_text_without_exit_code_is_not_rereading(self):
        self.assertEqual(self._three_reads("b", "cat: README.md: Permission denied")["count"], 0)   # исход «done»

    def test_reread_runs_cut_on_compaction_and_changed_content(self):
        t = [T0 + dt.timedelta(minutes=m) for m in range(6)]
        items = [({"at": t[0]}, "x"), ({"at": t[1]}, "x"), ({"at": t[2]}, "y"), ({"at": t[3]}, "y"), ({"at": t[5]}, "y")]
        self.assertEqual(L.reread_runs(items, []), [[items[2][0], items[3][0], items[4][0]]])
        self.assertEqual(L.reread_runs(items, [t[4]]), [])


class WaitsTest(unittest.TestCase):
    def test_async_question_while_agent_works_is_not_a_stop(self):
        t1, t2 = T0 + dt.timedelta(hours=1), T0 + dt.timedelta(hours=2)
        go, stop, pair = _sid(T0, "5"), _sid(t1, "6"), _sid(t2, "8")
        q = json.dumps({"questions": [{"question": "Какой формат?"}]})
        hooks = [_hook(_ts(T0, 0), "SessionStart", go, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", go, turn_id="T1", prompt="Собери отчёт"),
                 *_call(T0, go, 10, "q1", q, tool="request_user_input_async", turn_id="T1"),
                 *_call(T0, go, 20, "q2", q, tool="request_user_input_async", turn_id="T1"),
                 *[r for k in range(5) for r in _call(T0, go, 60 + k * 120, f"b{k}", '{"command":"ls"}', turn_id="T1")],
                 _hook(_ts(T0, 10 + 20 * 60), "UserPromptSubmit", go, turn_id="T1", prompt="таблицей"),
                 # агент стоял; вызов субагента в это время не делает основного агента занятым
                 _hook(_ts(t1, 0), "SessionStart", stop, source="startup"),
                 _hook(_ts(t1, 1), "UserPromptSubmit", stop, turn_id="T1", prompt="Собери отчёт"),
                 *_call(t1, stop, 10, "q1", q, tool="request_user_input_async", turn_id="T1"),
                 *_call(t1, stop, 100, "s1", '{"command":"ls"}', aid="sub-1", atype="default"),
                 _hook(_ts(t1, 10 + 10 * 60), "UserPromptSubmit", stop, turn_id="T1", prompt="таблицей"),
                 # два вопроса в одну секунду и один ответ: 10 мин, а не 20
                 _hook(_ts(t2, 0), "SessionStart", pair, source="startup"),
                 _hook(_ts(t2, 1), "UserPromptSubmit", pair, turn_id="T1", prompt="Собери отчёт"),
                 *_call(t2, pair, 10, "q1", q, tool="request_user_input_async", turn_id="T1"),
                 *_call(t2, pair, 10, "q2", q, tool="request_user_input_async", turn_id="T1"),
                 _hook(_ts(t2, 10 + 10 * 60), "UserPromptSubmit", pair, turn_id="T1", prompt="таблицей")]
        ds, tl = run(hooks)
        s = {x["id"]: x for x in ds["sessions"]}
        # два вопроса, агент работал: ни простоя, ни «вашего времени» от вопроса — не 40 и не 20 мин
        self.assertEqual((s[go]["waits"], s[go]["wait_min"], s[go]["user_min"]), (2, 0.0, 0.0))
        self.assertNotIn("wait", s[go]["flags"])
        self.assertEqual((s[stop]["waits"], s[stop]["wait_min"], s[stop]["user_min"]), (1, 10.0, 10.0))
        self.assertEqual((s[pair]["waits"], s[pair]["wait_min"]), (2, 10.0))
        wait = next(f for f in ds["friction"] if f["key"] == "wait")
        self.assertEqual(wait["by_session"], {stop: 1, pair: 2})
        q_ev = [e for e in tl[go]["events"] if e["k"] == "wait"]
        self.assertTrue(q_ev and all(e["side"] == "агент продолжал работу" for e in q_ev))
        card = next(f for f in ds["findings"] if f["pattern_id"] == "wait")
        self.assertEqual((card["readiness"], card["scope"]), ("hypothesis", "work"))
        self.assertTrue(card["preconditions"])

    def test_question_answered_inside_the_tool(self):
        # обычный (не async) вопрос держит агента до своего результата; ответ приходит результатом, без новой реплики
        sid = _sid(T0, "e")
        q = json.dumps({"questions": [{"question": "Какой формат?"}]})
        ans = '{"answers":["таблицей"]}'
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Собери отчёт"),
                 _hook(_ts(T0, 10), "PreToolUse", sid, tool="request_user_input", tuid="q1", tin=q, turn_id="T1"),
                 _hook(_ts(T0, 610), "PostToolUse", sid, tool="request_user_input", tuid="q1", tin=q, turn_id="T1",
                       resp_head=ans, resp_len=len(ans)),
                 *_call(T0, sid, 620, "b1", '{"command":"ls"}', turn_id="T1")]
        ds, tl = run(hooks)
        s = next(x for x in ds["sessions"] if x["id"] == sid)
        self.assertEqual(s["wait_min"], 10.0)
        self.assertIn("wait", s["flags"])
        q_ev = next(e for e in tl[sid]["events"] if e["k"] == "wait")
        self.assertEqual(q_ev["side"], "ждал 10.0 мин")

    def _question_session(self, c, *rows):
        """Сессия: ваша реплика, затем строки rows (время — секунды от T0)."""
        sid = _sid(T0, c)
        return sid, [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                     _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Собери отчёт"),
                     *[_hook(_ts(T0, sec), ev, sid, turn_id="T1", **kw) for sec, ev, kw in rows]]

    def test_question_without_pre_is_not_a_stop(self):
        # записан только PostToolUse вопроса: времени вопроса нет, интервал нулевой — не остановка
        q, ans = json.dumps({"questions": [{"question": "Какой формат?"}]}), '{"answers":["таблицей"]}'
        sid, hooks = self._question_session("1", (
            (10, "PostToolUse", dict(tool="request_user_input", tuid="q1", tin=q, resp_head=ans, resp_len=len(ans)))),
            (610, "UserPromptSubmit", dict(prompt="таблицей")))
        ds, tl = run(hooks)
        s = next(x for x in ds["sessions"] if x["id"] == sid)
        self.assertEqual((s["waits"], s["wait_min"]), (0, 0.0))
        self.assertNotIn("wait", s["flags"])
        self.assertEqual(next(e for e in tl[sid]["events"] if e["k"] == "wait")["side"], "вопрос без PreToolUse")

    def test_failed_question_is_not_a_stop(self):
        # Codex отказывает в request_user_input вне Plan mode: результат — сразу ошибка, вопрос не задан
        q, err = json.dumps({"questions": [{"question": "Какой формат?"}]}), '{"error":"request_user_input is only available in Plan mode"}'
        sid, hooks = self._question_session("2", (
            (10, "PreToolUse", dict(tool="request_user_input", tuid="q1", tin=q))),
            (11, "PostToolUse", dict(tool="request_user_input", tuid="q1", tin=q, resp_head=err, resp_len=len(err))),
            (610, "UserPromptSubmit", dict(prompt="продолжай")))
        ds, tl = run(hooks)
        s = next(x for x in ds["sessions"] if x["id"] == sid)
        self.assertEqual((s["waits"], s["wait_min"]), (1, 0.0))
        self.assertNotIn("wait", s["flags"])
        self.assertEqual(next(e for e in tl[sid]["events"] if e["k"] == "wait")["side"], "вопрос не прошёл")
        self.assertNotIn("агент спросил вас", {p["label"] for p in ds["permissions"]})

    def test_zero_length_question_is_not_a_stop(self):
        # результат в ту же секунду, что и вопрос: длина ожидания нулевая — не остановка
        q, ans = json.dumps({"questions": [{"question": "Какой формат?"}]}), '{"answers":["таблицей"]}'
        sid, hooks = self._question_session("5", (
            (10, "PreToolUse", dict(tool="request_user_input", tuid="q1", tin=q))),
            (10, "PostToolUse", dict(tool="request_user_input", tuid="q1", tin=q, resp_head=ans, resp_len=len(ans))),
            (610, "UserPromptSubmit", dict(prompt="продолжай")))
        s = next(x for x in run(hooks)[0]["sessions"] if x["id"] == sid)
        self.assertEqual((s["waits"], s["wait_min"]), (1, 0.0))
        self.assertNotIn("wait", s["flags"])

    def test_instant_text_result_is_not_a_stop(self):
        # отказ текстом через 50 мс (исход «done», не «error»): вопрос никто не ждал
        q, txt = json.dumps({"questions": [{"question": "Какой формат?"}]}), "Questions are not available in this mode."
        sid, hooks = self._question_session("6", (
            (10, "PreToolUse", dict(tool="request_user_input", tuid="q1", tin=q))),
            (10.05, "PostToolUse", dict(tool="request_user_input", tuid="q1", tin=q, resp_head=txt, resp_len=len(txt))),
            (610, "UserPromptSubmit", dict(prompt="продолжай")))
        ds, tl = run(hooks)
        s = next(x for x in ds["sessions"] if x["id"] == sid)
        self.assertEqual((s["waits"], s["wait_min"]), (1, 0.0))
        self.assertNotIn("wait", s["flags"])
        self.assertEqual(next(e for e in tl[sid]["events"] if e["k"] == "wait")["side"], "ответ сразу")

    def _inflight_session(self, c, post_s):
        """Вызов основного агента с 5 с (PostToolUse в post_s; None — нет), async-вопрос в 10 с, ваша реплика в 120 с."""
        q = json.dumps({"questions": [{"question": "Какой формат?"}]})
        rows = [(5, "PreToolUse", dict(tool="Bash", tuid="b1", tin='{"command":"pytest -q"}')),
                (10, "PreToolUse", dict(tool="request_user_input_async", tuid="q1", tin=q)),
                (11, "PostToolUse", dict(tool="request_user_input_async", tuid="q1", tin=q, resp_head="{}", resp_len=2)),
                (120, "UserPromptSubmit", dict(prompt="таблицей"))]
        if post_s is not None:
            rows.append((post_s, "PostToolUse", dict(tool="Bash", tuid="b1", tin='{"command":"pytest -q"}',
                                                     resp_head="1 passed", resp_len=8)))
        sid, hooks = self._question_session(c, *rows)
        ds, tl = run(hooks)
        return sid, ds, tl

    def test_tool_running_at_question_time_is_not_idle(self):
        # вызов шёл с 5 по 90 с, вопрос в 10 с, ответ в 120 с: агент стоял 30 с, а не 110
        sid, ds, _ = self._inflight_session("7", 90)
        s = next(x for x in ds["sessions"] if x["id"] == sid)
        self.assertEqual(s["wait_min"], 0.5)
        self.assertIn("wait", s["flags"])
        wait = next(f for f in ds["friction"] if f["key"] == "wait")
        self.assertIn("агент стоял 0.5 мин", wait["evidence"][0]["text"])

    def test_tool_running_without_result_is_not_provable_idle(self):
        sid, ds, _ = self._inflight_session("8", None)
        s = next(x for x in ds["sessions"] if x["id"] == sid)
        self.assertEqual(s["wait_min"], 0.0)
        self.assertNotIn("wait", s["flags"])

    def test_tool_ending_just_before_reply_is_not_a_stop(self):
        sid, ds, tl = self._inflight_session("9", 118)
        s = next(x for x in ds["sessions"] if x["id"] == sid)
        self.assertEqual(s["wait_min"], 0.0)
        self.assertNotIn("wait", s["flags"])
        self.assertEqual(next(e for e in tl[sid]["events"] if e["k"] == "wait")["side"], "агент продолжал работу")

    def _two_turn_session(self, c, lost_turn):
        """Ход T1, затем ход T2 с async-вопросом в 70 с и вашей репликой в 670 с; вызов без PostToolUse — в ходе lost_turn."""
        sid = _sid(T0, c)
        q = json.dumps({"questions": [{"question": "Какой формат?"}]})
        lost_at = 5 if lost_turn == "T1" else 65
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Собери отчёт"),
                 _hook(_ts(T0, lost_at), "PreToolUse", sid, turn_id=lost_turn, tool="Bash", tuid="b1",
                       tin='{"command":"pytest -q"}'),
                 _hook(_ts(T0, 60), "UserPromptSubmit", sid, turn_id="T2", prompt="Теперь таблицей"),
                 *_call(T0, sid, 70, "q1", q, tool="request_user_input_async", turn_id="T2"),
                 _hook(_ts(T0, 670), "UserPromptSubmit", sid, turn_id="T2", prompt="колонки по месяцам")]
        return next(x for x in run(hooks)[0]["sessions"] if x["id"] == sid)

    def test_unknown_end_from_earlier_turn_is_a_lost_result(self):
        # новый ход начинается с вашей реплики: вызов прошлого хода без PostToolUse — потерянный результат, не работа
        s = self._two_turn_session("a", "T1")
        self.assertEqual(s["wait_min"], 10.0)
        self.assertIn("wait", s["flags"])

    def test_unknown_end_in_the_same_turn_blocks_a_stop(self):
        s = self._two_turn_session("b", "T2")
        self.assertEqual(s["wait_min"], 0.0)
        self.assertNotIn("wait", s["flags"])

    def test_claude_failed_tool_ends_by_tool_result_duration(self):
        # у Claude упавший вызов приходит без PostToolUse, но с длительностью в tool_result: конец известен (5 + 85 с)
        q, ans = json.dumps({"questions": [{"question": "Какой формат?"}]}), '{"answers":["таблицей"]}'
        kw = dict(agent="claude", prompt_id="p1")
        rows = [_hook(_ts(T0, 1), "UserPromptSubmit", C, prompt="Собери отчёт", **kw),
                _hook(_ts(T0, 5), "PreToolUse", C, tool="Bash", tuid="b1", tin='{"command":"pytest -q"}', **kw),
                _hook(_ts(T0, 10), "PreToolUse", C, tool="AskUserQuestion", tuid="q1", tin=q, **kw),
                _hook(_ts(T0, 120), "PostToolUse", C, tool="AskUserQuestion", tuid="q1", tin=q, resp_head=ans,
                      resp_len=len(ans), **kw)]
        for r in rows:
            r["at"] = L.parse_ch_ts(r["ts"])
        S = L.build_session("claude", C, rows, claude=[{"e": "tool_result", "tuid": "b1", "success": "false",
                                                       "dur": "85000", "err": "exit 1", "at": T0 + dt.timedelta(seconds=90)}])
        self.assertEqual(S["wait_min"], 0.5)

    def test_blocking_question_ends_at_prompt_or_result_whichever_first(self):
        q, ans = json.dumps({"questions": [{"question": "Какой формат?"}]}), '{"answers":["таблицей"]}'
        sid, hooks = self._question_session("3", (
            (10, "PreToolUse", dict(tool="request_user_input", tuid="q1", tin=q))),
            (310, "UserPromptSubmit", dict(prompt="таблицей")),
            (610, "PostToolUse", dict(tool="request_user_input", tuid="q1", tin=q, resp_head=ans, resp_len=len(ans))))
        self.assertEqual(next(x for x in run(hooks)[0]["sessions"] if x["id"] == sid)["wait_min"], 5.0)

    def test_blocking_question_without_result_ends_at_prompt(self):
        q = json.dumps({"questions": [{"question": "Какой формат?"}]})
        sid, hooks = self._question_session("4", (
            (10, "PreToolUse", dict(tool="request_user_input", tuid="q1", tin=q))),
            (310, "UserPromptSubmit", dict(prompt="таблицей")))
        self.assertEqual(next(x for x in run(hooks)[0]["sessions"] if x["id"] == sid)["wait_min"], 5.0)

    def test_permission_wait_of_unknown_length_stays_unknown(self):
        sid = C   # any id; agent is claude
        rows = [dict(_hook(_ts(T0, 1), "UserPromptSubmit", sid, agent="claude", prompt_id="p1", prompt="Собери отчёт"))]
        for r in rows:
            r["at"] = L.parse_ch_ts(r["ts"])
        S = L.build_session("claude", sid, rows, claude=[{"e": "tool_decision", "dsource": "user", "decision": "accept",
                                                         "at": T0 + dt.timedelta(seconds=5)}])
        self.assertIsNone(S["wait_min"])


class CollectionFindingsTest(unittest.TestCase):
    def lost_card(self, ds):
        return next((x for x in ds["findings"] if x["title"].startswith("Найти, почему Codex не присылает PostToolUse")), None)

    def test_post_lost_in_sessions_started_under_hooks(self):
        sid = _sid(T0, "2")
        mcp = json.dumps({"query": "is:pr"})
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Найди мои PR")]
        for k in range(10):
            hooks += _call(T0, sid, 10 + k * 5, f"b{k}", '{"command":"ls"}', turn_id="T1")
        for k in range(4):
            hooks += _call(T0, sid, 80 + k * 5, f"m{k}", mcp, tool="mcp__codex_apps__github__search_prs", post=False, turn_id="T1")
        hooks += _call(T0, sid, 400, "last", '{"command":"ls"}', post=False, turn_id="T1")
        f = self.lost_card(run(hooks, now=T0 + dt.timedelta(days=1))[0])
        self.assertEqual(f["scope"], "collection")
        self.assertIn("у 5 из 15 вызовов нет PostToolUse", f["what"])
        self.assertIn("в последние 2 минуты своей сессии — 1", f["what"])
        self.assertIn("MCP codex_apps — 4", f["what"])
        self.assertNotIn("async", f["snip"])

    def test_grace_expires_for_finished_sessions(self):
        # контрпример из ревью: короткая завершённая сессия, три результата пропали в самом конце
        sid = _sid(T0, "7")
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Проверь сервис")]
        for k in range(3):
            hooks += _call(T0, sid, 10 + k * 10, f"x{k}", '{"command":"ls"}', post=False, turn_id="T1")
        self.assertIsNone(self.lost_card(run(hooks, now=T0 + dt.timedelta(seconds=60))[0]))   # результат ещё может прийти
        # ход открыт (нет Stop) и сессия молчит меньше часа: вызовы могут ещё выполняться
        self.assertIsNone(self.lost_card(run(hooks, now=T0 + dt.timedelta(minutes=30))[0]))
        f = self.lost_card(run(hooks, now=T0 + dt.timedelta(days=1))[0])
        self.assertIn("у 3 из 3 вызовов нет PostToolUse", f["what"])

    def test_otel_card_counts_sessions_once(self):
        # сессия с токенами из журнала (ход T1) и с codex.sse_event (ход T2), плюс сессия без токенов:
        # на плашке и в тексте — 1. Старая формула вычитала связанную сессию, уже исключённую как «из журнала», и давала 0.
        t1 = T0 + dt.timedelta(hours=1)
        both, none = _sid(T0, "9"), _sid(t1, "0")
        hooks = [_hook(_ts(T0, 0), "SessionStart", both, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", both, turn_id="T1", prompt="Посчитай"),
                 *_call(T0, both, 5, "a1", '{"command":"ls"}', turn_id="T1"),
                 _hook(_ts(T0, 20), "Stop", both, turn_id="T1", t_resp="1", t_in="100", t_cached="50", t_out="5",
                       t_model="gpt-6-sol"),
                 _hook(_ts(T0, 30), "UserPromptSubmit", both, turn_id="T2", prompt="Ещё раз"),
                 _hook(_ts(T0, 50), "Stop", both, turn_id="T2"),
                 _hook(_ts(t1, 0), "SessionStart", none, source="startup"),
                 _hook(_ts(t1, 1), "UserPromptSubmit", none, turn_id="T1", prompt="Посчитай ещё"),
                 *_call(t1, none, 5, "n1", '{"command":"ls"}', turn_id="T1")]
        sse = [{"ts": _ts(T0, 40), "sid": both, "kind": "response.completed", "model": "gpt-6-sol",
                "inp": "10", "cached": "5", "outp": "1", "reas": "0"}]
        otel = [{"svc": "Codex Desktop", "e": "codex.api_request", "endpoint": "/models", "linked": 0, "n": 5, "convs": 0}]
        ds, _ = run(hooks, sse=sse, codex_otel=otel)
        f = next(x for x in ds["findings"] if x["title"] == "Связать нативный OTel Codex с сессиями")
        self.assertEqual(f["sessions"], [none])
        self.assertEqual(f["impact"]["value"], "1 сессий")              # старая формула давала 0
        self.assertIn("У остальных 1 токены", f["what"])


class WorkFindingsTest(unittest.TestCase):
    def test_cards_are_hypotheses_and_skip_service_sessions(self):
        t1, t2, t3 = (T0 + dt.timedelta(hours=h) for h in (1, 2, 3))
        work, sug, close, spread = _sid(T0, "3"), _sid(t1, "4"), _sid(t2, "b"), _sid(t3, "c")

        def session(t, sid, prompt, gaps_s):
            rows = [_hook(_ts(t, 0), "SessionStart", sid, source="startup"),
                    _hook(_ts(t, 1), "UserPromptSubmit", sid, turn_id="T1", prompt=prompt),
                    *_call(t, sid, 10, f"{sid[-4:]}-1", '{"command":"ls"}', turn_id="T1")]
            return rows + [_hook(_ts(t, 100 + g), "PostCompact", sid, turn_id="T1", trig="auto") for g in gaps_s]

        hooks = (session(T0, work, "Собери отчёт по продажам", [0, 60, 120])            # три сжатия
                 + session(t1, sug, "# Overview\nGenerate 0 to 3 hyperpersonalized suggestions", [0, 60, 120])
                 + session(t2, close, "Почини сборку", [0, 5 * 60])                       # два сжатия за 5 мин
                 + session(t3, spread, "Обнови README", [0, 40 * 60]))                    # два сжатия, далеко — нет
        ds, _ = run(hooks)
        long = next(f for f in ds["findings"] if f["pattern_id"] == "compact")
        self.assertEqual(long["sessions"], sorted([work, close]))       # служебная и редкие сжатия не считаются
        self.assertEqual((long["readiness"], long["scope"]), ("hypothesis", "work"))
        self.assertTrue(long["preconditions"])
        self.assertEqual(long["impact_by_session"], {work: 3, close: 2})
        self.assertNotIn("сжатий на сессию", long["verification"])      # не метрика, которую даёт дробление
        self.assertFalse(any(f["readiness"] == "prepared" for f in ds["findings"]))

    def test_reread_card(self):
        sid = _sid(T0, "d")
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Разберись в документах")]
        step = iter(range(10, 10000, 10))
        for path in ("docs/README.md", "api/README.md", "docs/c.md"):      # два разных README.md не сливаются
            for _ in range(3):
                hooks += _call(T0, sid, next(step), f"t{len(hooks)}", json.dumps({"command": f"cat {path}"}),
                               resp="same", turn_id="T1")
        ds, _ = run(hooks)
        f = next(x for x in ds["findings"] if x["pattern_id"] == "reread")
        self.assertEqual((f["readiness"], f["scope"]), ("hypothesis", "work"))
        self.assertTrue(f["preconditions"])
        self.assertEqual(f["impact_by_session"], {sid: 6})              # по 2 лишних чтения на файл
        self.assertIn("docs/README.md — 2 в 1 сес.; api/README.md — 2 в 1 сес.", f["what"])


class NewestEvidenceTest(unittest.TestCase):
    """Период находки считается по времени доказательств: при обрезке остаются самые свежие, в порядке времени."""
    T1 = T0 + dt.timedelta(days=1)

    def assert_newest(self, ev, sid, at, n, first):
        """n доказательств по времени: последнее — (sid, at), первое оставленное — first (старшие отброшены)."""
        ats = [e["at"] for e in ev]
        self.assertEqual(len(ev), n)
        self.assertEqual(ats, sorted(ats))
        self.assertEqual((ev[-1]["sid"], ev[-1]["at"]), (sid, L.T.iso(at)))
        self.assertEqual(ev[0]["at"], L.T.iso(first))

    def test_compacts_keep_latest_episodes(self):
        # late начата раньше (в списке сессий первая), но её последнее сжатие — самое свежее: нужна сортировка по времени
        t_busy = T0 + dt.timedelta(hours=1)
        late, busy = _sid(T0, "1"), _sid(t_busy, "2")
        hooks = []
        for sid, t in ((late, T0), (busy, t_busy)):
            hooks += [_hook(_ts(t, 0), "SessionStart", sid, source="startup"),
                      _hook(_ts(t, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Собери отчёт"),
                      *_call(t, sid, 5, f"{sid[-4:]}-1", '{"command":"ls"}', turn_id="T1")]
        busy_at = [t_busy + dt.timedelta(seconds=100 + 60 * k) for k in range(61)]
        latest = self.T1 + dt.timedelta(seconds=100)
        late_at = [T0 + dt.timedelta(seconds=100), T0 + dt.timedelta(seconds=160), latest]
        hooks += [_hook(_ts(at, 0), "PostCompact", sid, turn_id="T1", trig="auto")
                  for sid, ats in ((late, late_at), (busy, busy_at)) for at in ats]
        ds, _ = run(hooks)
        fr = next(f for f in ds["friction"] if f["key"] == "compact")
        self.assertEqual(fr["count"], 64)
        self.assert_newest(fr["evidence"], late, latest, 60, busy_at[2])      # отброшены два сжатия late и два busy
        card = next(f for f in ds["findings"] if f["pattern_id"] == "compact")
        self.assert_newest(card["ev"], late, latest, 8, busy_at[54])

    def test_episode_keeps_latest_reads_and_mcp_errors(self):
        sid = _sid(T0, "3")
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Разберись в документах")]
        for k in range(10):
            hooks += _call(T0, sid, 10 + 10 * k, f"r{k}", json.dumps({"command": "cat docs/a.md"}), resp="same", turn_id="T1")
            hooks += _call(T0, sid, 200 + 10 * k, f"m{k}", "{}", tool="mcp__srv__search", resp="сбой",
                           resp_tail='{"isError":true}', turn_id="T1")
        ds, _ = run(hooks)
        fr = {f["key"]: f for f in ds["friction"]}
        self.assert_newest(fr["reread"]["evidence"], sid, T0 + dt.timedelta(seconds=10 + 10 * 9), 8,
                           T0 + dt.timedelta(seconds=10 + 10 * 2))
        self.assertTrue(fr["reread"]["evidence"][0]["text"].endswith("чтение 3/10"))
        self.assertTrue(fr["reread"]["evidence"][-1]["text"].endswith("чтение 10/10"))
        self.assert_newest(fr["mcpfail"]["evidence"], sid, T0 + dt.timedelta(seconds=200 + 10 * 9), 8,
                           T0 + dt.timedelta(seconds=200 + 10 * 2))

    def test_work_cards_keep_latest_episodes(self):
        # у старых эпизодов больше лишних чтений: раньше карточка брала крупнейшие и самые ранние
        def episode(sid, at, extra=None):
            e = {"sid": sid, "line": 1, "at": L.T.iso(at), "text": "эпизод", "_cost": None}
            return {"evidence": [e]} if extra is None else {"path": "/w/docs/a.md", "extra": extra, "bytes": 0, "evidence": [e]}

        old, new = _sid(T0, "4"), _sid(self.T1, "5")
        olds = [T0 + dt.timedelta(minutes=k) for k in range(9)]
        users = [{"sid": new, "compacts": [], "friction": {"reread": [episode(new, self.T1, 1)],    # новая — первой
                                                            "retry": [episode(new, self.T1)]}},
                 {"sid": old, "compacts": [], "friction": {"reread": [episode(old, t, 5) for t in olds],
                                                            "retry": [episode(old, t) for t in olds]}}]
        cards = {f["pattern_id"]: f for f in L.work_findings(users)}
        self.assert_newest(cards["reread"]["ev"], new, self.T1, 8, olds[2])
        self.assert_newest(cards["retry"]["ev"], new, self.T1, 8, olds[2])

    def test_collection_cards_keep_latest_sessions(self):
        # восемь больших старых сессий и одна маленькая новая: у всех есть вызовы без PostToolUse и Bash без кода выхода
        olds = [_sid(T0 + dt.timedelta(hours=k), str(k)) for k in range(8)]
        new = _sid(self.T1, "9")
        hooks = []
        for sid, t, done in [(s, T0 + dt.timedelta(hours=k), 3) for k, s in enumerate(olds)] + [(new, self.T1, 1)]:
            hooks += [_hook(_ts(t, 0), "SessionStart", sid, source="startup"),
                      _hook(_ts(t, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Проверь сборку")]
            for k in range(done):
                hooks += _call(t, sid, 10 + 10 * k, f"{sid[-4:]}-d{k}", '{"command":"ls"}', turn_id="T1")
            hooks += _call(t, sid, 100, f"{sid[-4:]}-lost", '{"command":"ls"}', post=False, turn_id="T1")
        otel = [{"svc": "Codex Desktop", "e": "codex.api_request", "endpoint": "/responses", "linked": 0, "n": 5, "convs": 0}]
        ds, _ = run(hooks, now=self.T1 + dt.timedelta(days=1), codex_otel=otel)

        def card(title):
            return next(f for f in ds["findings"] if f["title"].startswith(title))

        t4 = T0 + dt.timedelta(hours=4)                                  # пятая с конца сессия
        self.assert_newest(card("Найти, почему Codex не присылает PostToolUse")["ev"], new,
                           self.T1 + dt.timedelta(seconds=100), 8, T0 + dt.timedelta(hours=1, seconds=100))
        self.assert_newest(card("Связать нативный OTel Codex с сессиями")["ev"], new, self.T1 + dt.timedelta(seconds=100), 5,
                           t4 + dt.timedelta(seconds=100))
        self.assert_newest(card("Получать код выхода Bash")["ev"], new, self.T1 + dt.timedelta(seconds=10), 5,
                           t4 + dt.timedelta(seconds=30))

    def test_cards_cite_last_event_of_long_sessions(self):
        # три сессии начаты 1 сентября и продолжены 21-го: по три сбоя MCP (1, 1 и 21 сентября), токенов в OTel нет
        sep1, sep21 = dt.datetime(2026, 9, 1, 10, tzinfo=dt.timezone.utc), dt.datetime(2026, 9, 21, 10, tzinfo=dt.timezone.utc)
        sids = [_sid(sep1 + dt.timedelta(minutes=k), c) for k, c in enumerate("abc")]
        mcp = dict(tool="mcp__srv__search", resp="сбой", resp_tail='{"isError":true}', turn_id="T1")
        hooks = []
        for k, sid in enumerate(sids):
            t = sep1 + dt.timedelta(minutes=k)
            hooks += [_hook(_ts(t, 0), "SessionStart", sid, source="startup"),
                      _hook(_ts(t, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Найди задачи"),
                      *_call(t, sid, 10, f"{sid[-4:]}-1", "{}", **mcp), *_call(t, sid, 20, f"{sid[-4:]}-2", "{}", **mcp),
                      *_call(sep21, sid, k * 60, f"{sid[-4:]}-3", "{}", **mcp)]
        otel = [{"svc": "Codex Desktop", "e": "codex.api_request", "endpoint": "/responses", "linked": 0, "n": 5, "convs": 0}]
        ds, tl = run(hooks, now=sep21 + dt.timedelta(days=1), codex_otel=otel)
        mf = next(f for f in ds["findings"] if f["pattern_id"] == "mcpfail")
        self.assertEqual([e["at"][:10] for e in mf["ev"]], ["2026-09-21"] * 3)
        otel_card = next(f for f in ds["findings"] if f["title"] == "Связать нативный OTel Codex с сессиями")
        self.assertEqual(sorted((e["sid"], e["at"], e["line"]) for e in otel_card["ev"]),
                         sorted((sid, tl[sid]["events"][-1]["at"], tl[sid]["events"][-1]["line"]) for sid in sids))
        self.assertEqual({e["at"][:10] for e in otel_card["ev"]}, {"2026-09-21"})


if __name__ == "__main__":
    unittest.main()
