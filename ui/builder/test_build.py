"""Tests for telemetry.py and build.py on a small synthetic rollout (invented content only).

Run: cd ui/builder && python3 -m unittest -v
"""

import argparse
import datetime as dt
import json
import os
import shutil
import stat
import sys
import tempfile
import types
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import build  # noqa: E402
import telemetry as T  # noqa: E402

SID = "00000000-0000-4000-8000-00000000f1a7"
FIXTURE_DIR = HERE / "testdata" / "telemetry"
FIXTURE = FIXTURE_DIR / f"rollout-2026-01-10T10-00-00-{SID}.jsonl"


def call(P, call_id):
    return next(c for c in P["calls"] if c["call_id"] == call_id)


class ParseFixture(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.P = T.parse_rollout(FIXTURE)
        cls.S = T.session_core(cls.P)
        cls.TL = T.session_timeline(cls.P)

    # turns / prompts / tokens -------------------------------------------------
    def test_turns_prompts_requests(self):
        self.assertEqual(self.S["id"], SID)
        self.assertEqual(self.S["turns"], 3)
        self.assertEqual(self.S["prompts"], 4)
        self.assertEqual(self.S["reqs"], 4)
        self.assertEqual(self.S["project"], "demo-project")
        self.assertEqual(self.S["model"], "gpt-6-astra")
        states = [t["state"] for t in self.TL["turns"]]
        self.assertEqual(states, ["task_complete", "turn_aborted", "task_complete"])
        self.assertEqual(self.TL["turns"][0]["prompt_line"], 5)
        self.assertEqual(self.TL["turns"][0]["dur_ms"], 210000)

    def test_tokens_and_cost(self):
        self.assertEqual(self.S["tok"], {"input": 16000, "cached": 5400, "output": 380, "reasoning": 30})
        self.assertEqual(self.S["cache_hit"], 0.338)
        # reasoning is part of output: (16000-5400)*1.25 + 5400*0.125 + 380*10 per 1M
        self.assertAlmostEqual(self.S["cost_usd"], 0.017725, places=4)
        self.assertAlmostEqual(self.TL["cum"][-1][1], 0.017725, places=4)
        self.assertEqual(self.TL["turns"][0]["tok"], {"input": 3000, "cached": 1500, "output": 150})

    def test_first_prompt_and_reply(self):
        self.assertTrue(self.S["first"].startswith("Please fix the demo widget."))
        reply = self.P["prompts"][1]
        self.assertEqual(reply["kind"], "reply")
        self.assertEqual(reply["text"], "ответ на вопрос агента: blue")

    # tool errors ------------------------------------------------------------------
    def test_tool_error_detection(self):
        self.assertEqual(self.S["calls"], 13)
        self.assertEqual(self.S["errors"], 10)
        self.assertEqual(self.S["unknown_results"], 0)
        self.assertEqual(call(self.P, "c1")["state"], "success")
        c2 = call(self.P, "c2")
        self.assertEqual(c2["state"], "error")
        self.assertTrue(c2["note"].startswith("exit 1"))
        self.assertIn("AssertionError", c2["note"])
        self.assertEqual(c2["dur_s"], 2.0)          # from "Wall time"
        self.assertEqual(call(self.P, "m1")["state"], "error")   # MCP failed / isError
        self.assertEqual(call(self.P, "c6")["state"], "success")  # apply_patch FileChange completed
        self.assertEqual(call(self.P, "p1")["state"], "error")   # exit_code 2 only in the output chunk
        errs = [e for e in self.TL["events"] if e["k"] == "err"]
        self.assertEqual(len(errs), 10)
        self.assertEqual(errs[0]["x"], "pytest -q tests/test_widget.py")

    def test_exec_title_counts_extra_commands(self):
        tool = next(e for e in self.TL["events"] if e["line"] == 7)
        self.assertEqual(tool["k"], "tool")
        self.assertTrue(tool["x"].endswith(" +1"))
        self.assertEqual(tool["side"], "1.5 с")

    # flags ----------------------------------------------------------------------------
    def test_compaction_abort_wait(self):
        self.assertEqual(self.S["compactions"], 1)
        self.assertEqual(self.S["aborted"], 1)
        self.assertEqual(self.S["waits"], 1)
        self.assertEqual(self.S["wait_min"], 3.0)
        self.assertEqual(self.S["flags"], ["retry", "coldcache", "compact", "wait", "abort"])
        kinds = {e["k"] for e in self.TL["events"]}
        self.assertTrue({"prompt", "answer", "tool", "err", "api", "wait", "compact", "abort", "skill"} <= kinds)

    def test_user_min_away_rule(self):
        # 3.0 min (question -> reply) + 10.17 min (answer -> next prompt); 13.7 h gap before turn 3 is "away"
        self.assertEqual(self.S["user_min"], 13.2)
        self.assertEqual(self.P["away_gaps"], 1)

    def test_coldcache(self):
        items = self.P["friction"]["coldcache"]
        self.assertEqual(len(items), 1)
        self.assertEqual(items[0]["lines"], [32])
        self.assertAlmostEqual(items[0]["cost"], 9000 * (1.25 - 0.125) / 1e6, places=9)

    def test_end_ignores_passive_records_and_partial_line(self):
        self.assertEqual(self.S["end"], "2026-01-11T00:01:00.000Z")
        self.assertEqual(self.P["bad_lines"], [58])
        self.assertEqual(self.S["records"], 58)
        self.assertEqual(self.S["wall_min"], 841.0)

    def test_daily_split(self):
        daily = {d["date"]: d for d in self.S["daily"]}
        self.assertEqual(sorted(daily), ["2026-01-10", "2026-01-11"])
        self.assertEqual(daily["2026-01-11"]["reqs"], 1)
        self.assertEqual(daily["2026-01-10"]["reqs"], 3)
        self.assertEqual(daily["2026-01-10"]["agent_min"], 5.0)   # 3.5 + 0.52 + 1.0 before midnight
        self.assertEqual(daily["2026-01-11"]["agent_min"], 1.0)
        self.assertEqual(daily["2026-01-10"]["calls"], 13)
        self.assertEqual(daily["2026-01-10"]["errors"], 10)
        self.assertEqual(daily["2026-01-10"]["user_min"], 13.2)

    # retry detector ----------------------------------------------------------------------
    def test_retry_positive_and_negatives(self):
        episodes = self.P["friction"]["retry"]
        self.assertEqual([e["lines"] for e in episodes], [[12, 15, 18]])
        polled = {call(self.P, x)["line"] for x in ("p1", "p2", "p3")}
        self.assertTrue(all(call(self.P, x)["is_wait"] for x in ("p1", "p2", "p3")))
        self.assertFalse(polled & set(episodes[0]["lines"]))

    def test_retry_without_edit_between_is_detected(self):
        # Drop the apply_patch call (lines 42-44): the three failing `cat missing.txt` calls become a retry.
        lines = FIXTURE.read_text().splitlines(keepends=True)
        tmp = Path(tempfile.mkdtemp())
        try:
            path = tmp / FIXTURE.name
            path.write_text("".join(lines[:41] + lines[44:]))
            P = T.parse_rollout(path)
            self.assertEqual(len(P["friction"]["retry"]), 2)
        finally:
            shutil.rmtree(tmp)

    # skills ------------------------------------------------------------------------------
    def test_skill_activation(self):
        self.assertEqual(len(self.P["skills_available"]), 3)
        self.assertEqual([(a["name"], a["line"]) for a in self.P["skill_acts"]], [("alpha", 8)])
        sk = T.aggregate_skills([self.P])
        self.assertEqual(sk["available"], 3)
        self.assertEqual(sk["rows"][0]["name"], "alpha")
        self.assertEqual(sk["rows"][0]["activations"], 1)
        self.assertEqual(sk["rows"][0]["by_session"], {SID: 1})      # интерфейс пересчитывает активации под фильтр
        for r in sk["rows"]:
            self.assertEqual(sum(r["by_session"].values()), r["activations"])
        self.assertEqual(sk["rows"][-1]["state"], "unused")
        self.assertIn("остальные 2", sk["rows"][-1]["source"])
        self.assertEqual(sk["gantt"]["sid"], SID)
        self.assertEqual(sk["gantt"]["rows"][0]["name"], "alpha")

    def test_skill_md_reads(self):
        self.assertEqual(T.skill_md_reads("sed -n '1,80p' /a/b/SKILL.md"), ["/a/b/SKILL.md"])
        self.assertEqual(T.skill_md_reads("cd /x && cat SKILL.md", cwd="/x/s"), ["/x/s/SKILL.md"])
        self.assertTrue(T.skill_md_reads("head -n 40 ~/skills/z/SKILL.md | tail -5")[0].endswith("/skills/z/SKILL.md"))
        self.assertEqual(T.skill_md_reads("rg -n 'alpha.*(SKILL.md' notes.md"), [])
        self.assertEqual(T.skill_md_reads("rg --files -g 'SKILL.md' ."), [])
        self.assertEqual(T.skill_md_reads("rg -n SKILL.md docs/"), [])      # pattern, not a file
        self.assertEqual(T.skill_md_reads("wc -l /a/b/SKILL.md"), [])

    # line -> timestamp ---------------------------------------------------------------------
    def test_line_at(self):
        self.assertEqual(T.line_at(self.P, 5), "2026-01-10T10:00:02.000Z")
        self.assertEqual(T.line_at(self.P, "12"), "2026-01-10T10:00:10.000Z")
        self.assertEqual(T.line_at(self.P, 58), "2026-01-12T00:00:00.000Z")   # broken line -> record before
        self.assertEqual(T.line_at(self.P, 9999), "2026-01-12T00:00:00.000Z")
        self.assertIsNone(T.line_at(self.P, 0))
        self.assertTrue(all(e["line"] and e["at"] for e in self.TL["events"]))

    # redaction ------------------------------------------------------------------------------
    def test_redaction_in_outputs(self):
        blob = json.dumps(self.TL, ensure_ascii=False) + json.dumps(self.S, ensure_ascii=False)
        self.assertNotIn("ABCDEF1234567890SECRET", blob)
        self.assertNotIn("sk-abcdefghijklmnop1234", blob)
        self.assertIn("api_key=[скрыто]", self.S["first"])

    def test_redact_patterns(self):
        r = T.redact
        self.assertEqual(r("password: hunter2"), "password: [скрыто]")
        self.assertEqual(r("Authorization: Bearer abc.def-ghi123"), "Authorization: [скрыто]")
        self.assertEqual(r("curl -H 'X: Bearer abcdef123456'"), "curl -H 'X: Bearer [скрыто]'")
        self.assertEqual(r("key sk-ABCdef1234567890"), "key [скрыто]")
        self.assertEqual(r("sha " + "a" * 40), "sha [скрыто]")
        self.assertEqual(r("blob QmFzZTY0QmxvYkRhdGFXaXRoTnVtYmVyczEyMzQ1Ng=="), "blob [скрыто]")
        keep = ["/Users/demo/projects/some-long-project-name/src/module_file.py",
                "00000000-0000-4000-8000-00000000f1a7", "rollout-2026-01-10T10-00-00-notes"]
        for text in keep:
            self.assertEqual(r(text), text)
        self.assertEqual(T.clean("x" * 300, 50)[-1], "…")
        self.assertEqual(len(T.clean("x" * 300, 50)), 50)

    # command parsing -----------------------------------------------------------------------
    def test_js_cmds_and_normalize(self):
        js = 'await Promise.allSettled([\n["a", {cmd:"git -C repo status --short"}],\n' \
             "[\"b\", {'cmd':'rg -n \\'x\\' .'}],\n(async()=>text(await tools.exec_command({cmd:\"echo \\\"hi\\\"\"})))()]);"
        self.assertEqual(T.extract_js_cmds(js), ["git -C repo status --short", "rg -n 'x' .", 'echo "hi"'])
        cases = {"cd x && uv run pytest -q": "uv run pytest", "DEVELOPER_DIR=/x xcodebuild -scheme A test": "xcodebuild test",
                 "sed -n '1,20p' f": "sed -n", "[ -f a ] && cat a": "cat", "python3 - <<'PY'\nprint(1)\nPY": "python3 (скрипт)",
                 "/opt/homebrew/bin/gh pr create --title t": "gh pr create", "rg -n foo": "rg"}
        for cmd, want in cases.items():
            self.assertEqual(T.normalize_cmd(cmd), want, cmd)
        self.assertTrue(T.is_test_cmd("cd a && python3 -m pytest -q"))
        self.assertFalse(T.is_test_cmd("cat tests/README.md"))


class Aggregates(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.P = T.parse_rollout(FIXTURE)
        cls.S = T.session_core(cls.P)

    def test_by_session_everywhere(self):
        tools = T.aggregate_tools([self.P])
        exec_row = next(r for r in tools if r["name"] == "exec")
        self.assertEqual(exec_row["by_session"][SID], {"calls": 11, "errors": 9, "unknown": 0})
        mcp = T.aggregate_mcp([self.P])
        self.assertEqual(mcp[0]["server"], "demo_mcp")
        self.assertEqual(mcp[0]["by_session"][SID], {"calls": 1, "errors": 1, "unknown": 0})
        cmds = T.aggregate_commands([self.P], [self.S])
        pytest_row = next(r for r in cmds if r["cmd"] == "pytest")
        self.assertEqual((pytest_row["runs"], pytest_row["failed"]), (3, 3))
        self.assertEqual(pytest_row["by_session"][SID], {"runs": 3, "failed": 3, "unknown": 0})
        perms = {r["label"]: r for r in T.aggregate_permissions([self.P])}
        self.assertEqual(perms["политика never · без запроса"]["by_session"][SID], 13)
        self.assertEqual(perms["агент спросил вас"]["count"], 1)

    def test_by_session_carries_unknown(self):
        # одна сессия: значения по сессии совпадают с итогами строки, в том числе unknown
        old = T.parse_rollout(OLD_FIXTURE)                      # в ней есть вызовы и команды без результата
        for P, S in ((self.P, self.S), (old, T.session_core(old))):
            sid = P["sid"]
            tools = T.aggregate_tools([P])
            for r in tools:
                self.assertEqual(r["by_session"][sid], {"calls": r["calls"], "errors": r["errors"], "unknown": r["unknown"]})
            top = sum(r["by_session"][sid]["unknown"] for r in tools if r["kind"] != "nested")
            self.assertEqual(top, S["unknown_results"])
            for r in T.aggregate_mcp([P]):
                self.assertEqual(r["by_session"][sid], {"calls": r["calls"], "errors": r["errors"], "unknown": r["unknown"]})
            for r in T.aggregate_commands([P], [S]):
                self.assertEqual(r["by_session"][sid], {"runs": r["runs"], "failed": r["failed"], "unknown": r["unknown"]})
        self.assertEqual(sum(r["by_session"][OLD_SID]["unknown"] for r in T.aggregate_tools([old])), 5)
        self.assertEqual(sum(r["by_session"][OLD_SID]["unknown"] for r in T.aggregate_commands([old], [T.session_core(old)])), 3)

    def test_mcp_unknown_is_one_rule(self):
        # McpToolCall без статуса завершения: исход неизвестен и в «Инструментах», и в «MCP»
        sid = "00000000-0000-4000-8000-0000000000b1"
        items = [{"server": "srv", "tool": "search", "status": s, "failed": s == "failed", "direct": False, "dur_s": None}
                 for s in ("in_progress", "completed", "failed")]
        P = {"sid": sid, "calls": [], "ces": [], "edits": [], "mcp_items": items, "nested": []}
        (m,) = T.aggregate_mcp([P])
        (t,) = T.aggregate_tools([P])
        self.assertEqual(t["name"], "srv.search")
        for r in (m, t):
            self.assertEqual((r["calls"], r["errors"], r["unknown"]), (3, 1, 1))
            self.assertEqual(r["by_session"][sid], {"calls": 3, "errors": 1, "unknown": 1})

    def test_friction_by_session(self):
        fr = {f["key"]: f for f in T.aggregate_friction([self.P])}
        self.assertEqual(fr["retry"]["by_session"], {SID: 1})
        self.assertEqual(fr["correction"]["by_session"], {})
        for f in fr.values():
            self.assertEqual(sum(f["by_session"].values()), f["count"] or 0)

    def test_correction_friction_from_classes(self):
        fr = {f["key"]: f for f in T.aggregate_friction([self.P], {SID: {1: "correction", 0: "task"}})}
        self.assertEqual(fr["correction"]["count"], 1)
        self.assertEqual(fr["correction"]["evidence"][0]["line"], 31)   # prompt line of turn 1
        none = {f["key"]: f for f in T.aggregate_friction([self.P])}
        self.assertIsNone(none["correction"]["count"])
        self.assertEqual(none["retry"]["count"], 1)
        self.assertAlmostEqual(none["coldcache"]["cost_usd"], 0.0101, places=4)


class NewestEvidenceTest(unittest.TestCase):
    """Период находки считается по времени доказательств: при обрезке остаются самые свежие, в порядке времени."""
    T0 = dt.datetime(2026, 3, 8, 10, 0, tzinfo=dt.timezone.utc)
    T1 = T0 + dt.timedelta(days=1)
    OLD, NEW = "00000000-0000-4000-8000-0000000000a1", "00000000-0000-4000-8000-0000000000a2"

    def assert_newest(self, ev, sid, at, n, first):
        """n доказательств по времени: последнее — (sid, at), первое оставленное — first (старшие отброшены)."""
        ats = [e["at"] for e in ev]
        self.assertEqual(len(ev), n)
        self.assertEqual(ats, sorted(ats))
        self.assertEqual((ev[-1]["sid"], ev[-1]["at"]), (sid, T.iso(at)))
        self.assertEqual(ev[0]["at"], T.iso(first))

    def test_newest(self):
        def e(name, minute):
            return {"text": name, "at": None if minute is None else T.iso(self.T0 + dt.timedelta(minutes=minute))}

        ev = [e("c", 3), e("none", None), e("a", 1), e("b1", 2), e("b2", 2), e("d", 4)]
        self.assertEqual([x["text"] for x in T.newest(ev, 3)], ["b2", "c", "d"])     # при равном времени — более поздний
        self.assertEqual([x["text"] for x in T.newest(ev, 5)], ["a", "b1", "b2", "c", "d"])   # без времени — первым
        self.assertEqual([x["text"] for x in T.newest(ev, 10)], ["none", "a", "b1", "b2", "c", "d"])
        self.assertEqual(T.newest(ev, 0), [])
        self.assertEqual([x["text"] for x in ev], ["c", "none", "a", "b1", "b2", "d"])          # вход не меняется

    def test_friction_keeps_latest_episodes(self):
        def episodes(sid, ats):
            return [{"lines": [k + 1], "evidence": [{"sid": sid, "line": k + 1, "at": T.iso(at), "text": "сжатие", "_cost": None}]}
                    for k, at in enumerate(ats)]

        old_ats = [self.T0 + dt.timedelta(minutes=k) for k in range(61)]
        parsed = [{"sid": self.NEW, "friction": {"compact": episodes(self.NEW, [self.T1])}},   # новая — первой: нужна сортировка
                  {"sid": self.OLD, "friction": {"compact": episodes(self.OLD, old_ats)}}]
        fr = {f["key"]: f for f in T.aggregate_friction(parsed)}
        self.assertEqual(fr["compact"]["count"], 62)
        self.assert_newest(fr["compact"]["evidence"], self.NEW, self.T1, 60, old_ats[2])

    def test_episode_keeps_latest_reads_and_mcp_errors(self):
        at = [self.T0 + dt.timedelta(minutes=k) for k in range(20)]
        reads = [{"line": 10 + k, "at": at[k], "cmd": "cat docs/a.md", "parsed": [{"type": "read", "path": "docs/a.md"}],
                  "is_test": False} for k in range(10)]
        fails = [{"line": 30 + k, "at": at[10 + k], "server": "srv", "tool": "search", "failed": True, "err": "сбой"}
                 for k in range(10)]
        P = {"sid": self.OLD, "calls": [], "ces": reads, "edits": [], "mcp_items": fails, "api": [], "prompts": [],
             "compactions": [], "waits": [], "aborts": [], "prices": {}}
        fr = T._session_friction(P)
        (rr,), (mf,) = fr["reread"], fr["mcpfail"]
        self.assert_newest(rr["evidence"], self.OLD, at[9], 8, at[2])
        self.assertTrue(rr["evidence"][0]["text"].endswith("чтение 3/10"))
        self.assertTrue(rr["evidence"][-1]["text"].endswith("чтение 10/10"))
        self.assert_newest(mf["evidence"], self.OLD, at[19], 8, at[12])


class BuildIntegration(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        (self.tmp / "reports").mkdir()
        (self.tmp / "tests").mkdir()
        self.saved = sys.modules.get("conclusions", "missing")

    def tearDown(self):
        shutil.rmtree(self.tmp)
        if self.saved == "missing":
            sys.modules.pop("conclusions", None)
        else:
            sys.modules["conclusions"] = self.saved

    def args(self):
        return argparse.Namespace(ids=SID, sessions_root=str(FIXTURE_DIR), reports=str(self.tmp / "reports"),
                                  tests=str(self.tmp / "tests"), out=str(self.tmp / "out"))

    def test_fallback_without_conclusions(self):
        sys.modules["conclusions"] = None   # makes `import conclusions` raise ImportError
        dataset, out = build.build(self.args())
        self.assertEqual(stat.S_IMODE(os.stat(out).st_mode), 0o700)
        self.assertEqual(stat.S_IMODE(os.stat(out / "sessions").st_mode), 0o700)
        for f in (out / "dataset.json", out / "sessions" / f"{SID}.json"):
            self.assertEqual(stat.S_IMODE(os.stat(f).st_mode), 0o600)
            json.loads(f.read_text())
        s = dataset["sessions"][0]
        self.assertIsNone(s["corrections"])
        self.assertEqual(dataset["checks_catalog"], [])
        self.assertTrue(any("conclusions.py недоступен" in g for g in dataset["gaps"]))
        self.assertEqual(dataset["pricing"]["models"]["gpt-6-astra"]["cached"], 0.125)

    def test_merge_with_conclusions(self):
        fake = types.ModuleType("conclusions")
        fake.load_catalogue = lambda: [{"id": "D01", "name": "Ранняя коррекция", "how": "…"}]

        def load_session(sid, reports, tests):
            return {"session_fields": {"title": "Починить виджет", "outcome": "partial", "turns": 999,
                                       "checks": [{"id": "D01", "status": "suspected", "summary": "…", "lines": [12, "L15"]}],
                                       "sources": {"deep": "v2", "test_review": True}},
                    "findings": [{"id": "x", "sev": "warn", "sessions": [sid],
                                  "ev": [{"sid": sid, "line": 12, "at": None, "text": "…"}, {"sid": sid, "line": 58, "at": None}]}],
                    "human_turn_classes": {"1": "correction", "0": "task"},
                    "episodes": [{"pattern_id": "D01", "lines": [12, 31], "text": "…"}],
                    "gaps": ["тестовый пробел"]}
        fake.load_session = load_session
        sys.modules["conclusions"] = fake
        dataset, out = build.build(self.args())
        s = dataset["sessions"][0]
        self.assertEqual(s["title"], "Починить виджет")
        self.assertEqual(s["turns"], 3)                      # telemetry numbers are kept
        self.assertEqual(s["corrections"], 1)
        self.assertIn("correction", s["flags"])
        self.assertEqual(s["sources"]["deep"], "v2")
        self.assertEqual(s["sources"]["transcript"], "available")
        self.assertEqual(s["checks"][0]["lines"], [12, 15])
        self.assertEqual(s["checks"][0]["at"], ["2026-01-10T10:00:10.000Z", "2026-01-10T10:00:14.000Z"])
        ev = dataset["findings"][0]["ev"]
        self.assertEqual(ev[0]["at"], "2026-01-10T10:00:10.000Z")
        self.assertEqual(ev[1]["at"], "2026-01-12T00:00:00.000Z")   # unparsed line -> nearest record before
        fr = {f["key"]: f for f in dataset["friction"]}
        self.assertEqual(fr["correction"]["count"], 1)
        self.assertEqual(dataset["checks_catalog"][0]["id"], "D01")
        timeline = json.loads((out / "sessions" / f"{SID}.json").read_text())
        self.assertEqual(timeline["episodes"][0]["at"][1], "2026-01-10T10:13:41.000Z")
        self.assertTrue(any("тестовый пробел" in g for g in dataset["gaps"]))

    def test_load_all_is_preferred(self):
        other = "00000000-0000-4000-8000-0000000000ee"   # not built: its evidence keeps at = null
        fake = types.ModuleType("conclusions")
        fake.load_catalogue = lambda: []

        def load_session(sid, reports, tests):
            raise AssertionError("load_session must not be called when load_all exists")

        def load_all(ids, reports, tests):
            self.assertEqual(ids, [SID])
            card = {"id": "p2:reg", "sev": "bad", "registry": True, "sessions": [SID, other],
                    "ev": [{"sid": SID, "line": 12, "at": None, "text": "…"}, {"sid": other, "line": 3, "at": None, "text": "…"}]}
            session = {"session_fields": {"title": "Из реестра", "sources": {"deep": "v2", "findings_from": "registry"}},
                       "findings": [card], "human_turn_classes": {}, "episodes": [], "gaps": ["пробел сессии"]}
            return {"sessions": {SID: session}, "findings": [card], "gaps": ["общий пробел"]}

        fake.load_session = load_session
        fake.load_all = load_all
        sys.modules["conclusions"] = fake
        dataset, _ = build.build(self.args())
        self.assertEqual(dataset["sessions"][0]["title"], "Из реестра")
        self.assertEqual(dataset["sessions"][0]["sources"]["findings_from"], "registry")
        self.assertEqual([f["id"] for f in dataset["findings"]], ["p2:reg"])   # not duplicated from the session dict
        ev = dataset["findings"][0]["ev"]
        self.assertEqual(ev[0]["at"], "2026-01-10T10:00:10.000Z")
        self.assertIsNone(ev[1]["at"])
        self.assertIn("общий пробел", dataset["gaps"])
        self.assertTrue(any("пробел сессии" in g for g in dataset["gaps"]))

    def test_load_all_failure_falls_back_to_sessions(self):
        fake = types.ModuleType("conclusions")
        fake.load_catalogue = lambda: []

        def load_all(ids, reports, tests):
            raise RuntimeError("boom")

        fake.load_all = load_all
        fake.load_session = lambda sid, reports, tests: {
            "session_fields": {"title": "По сессии"}, "findings": [{"id": "s", "sessions": [sid], "ev": []}],
            "human_turn_classes": {}, "episodes": [], "gaps": []}
        sys.modules["conclusions"] = fake
        dataset, _ = build.build(self.args())
        self.assertEqual(dataset["sessions"][0]["title"], "По сессии")
        self.assertEqual([f["id"] for f in dataset["findings"]], ["s"])
        self.assertTrue(any("Общий сбор выводов не удался" in g for g in dataset["gaps"]))


class HelpersTest(unittest.TestCase):
    def test_discover_ids(self):
        with tempfile.TemporaryDirectory() as tmp:
            reports, tests = Path(tmp, "reports"), Path(tmp, "tests")
            a, b, c = ("00000000-0000-4000-8000-00000000000" + x for x in "abc")
            for d, name in ((reports / "v2" / "deep", a), (reports / "deep", b), (reports / "telemetry", "manifest")):
                d.mkdir(parents=True, exist_ok=True)
                Path(d, name + ".json").write_text("{}")
            Path(tests, c).mkdir(parents=True)
            self.assertEqual(build.discover_ids(reports, tests), [a, b, c])
            self.assertEqual(build.discover_ids(Path(tmp, "none"), Path(tmp, "none")), build.DEFAULT_IDS)

    def test_read_span(self):
        import telemetry
        self.assertEqual(telemetry.read_span("sed -n '1,200p' a.py"), "строки 1–200")
        self.assertEqual(telemetry.read_span(["bash", "-lc", "nl -ba a | sed -n 40,80p"]), "строки 40–80")
        self.assertEqual(telemetry.read_span("head -n 50 a"), "первые 50")
        self.assertEqual(telemetry.read_span("cat a"), "весь файл")

    def test_project_name_for_chatgpt_mirror(self):
        import telemetry
        with tempfile.TemporaryDirectory() as tmp:
            mirror = os.path.join(tmp, "g-p-0123456789abcdef0123")
            os.mkdir(mirror)
            self.assertEqual(telemetry.project_name(mirror), "g-p-0123456789abcdef0123")
            Path(mirror, "AGENTS.md").write_text("# ChatGPT project context\n\nThis directory is a local mirror of the ChatGPT project “Пекарня”.\n")
            self.assertEqual(telemetry.project_name(mirror), "Пекарня")
            self.assertEqual(telemetry.project_name("/work/bakery/"), "bakery")
            self.assertIsNone(telemetry.project_name(None))


OLD_SID = "00000000-0000-4000-8000-0000000001d0"
OLD_FIXTURE = FIXTURE_DIR / f"rollout-2026-02-01T09-00-00-{OLD_SID}.jsonl"


class OldFormatFixture(unittest.TestCase):
    """Old rollouts (gpt-5.6-sol era): no token_usage_record, js-kernel `wait`, direct exec_command/apply_patch,
    McpToolCall/ContextCompaction items without started_at_ms."""

    @classmethod
    def setUpClass(cls):
        cls.P = T.parse_rollout(OLD_FIXTURE)
        cls.S = T.session_core(cls.P)
        cls.TL = T.session_timeline(cls.P)

    def test_tokens_from_token_count(self):
        self.assertEqual(self.S["reqs"], 5)                   # info=null and the duplicate poll are skipped
        self.assertEqual(self.S["tok"], {"input": 5200, "cached": 2900, "output": 150, "reasoning": 15})
        ts = self.P["tok_stat"]
        self.assertEqual((ts["tur"], ts["tc_used"], ts["tc_dup"], ts["resets"]), (0, 5, 1, 1))
        self.assertEqual(ts["tc_sum"], ts["final_sum"])       # equals final total_token_usage across the reset
        self.assertAlmostEqual(self.S["cost_usd"], ((5200 - 2900) * 1.25 + 2900 * 0.125 + 150 * 10) / 1e6, places=4)
        api = [e for e in self.TL["events"] if e["k"] == "api"]
        self.assertEqual(len(api), 5)
        self.assertTrue(api[0]["x"].startswith("gpt-5.6-sol · вход 1 тыс."))
        self.assertIn("из token_count", api[0]["note"])
        self.assertEqual(len(self.TL["cum"]), 6)

    def test_assumed_price_for_every_model(self):
        self.assertIn("gpt-5.6-sol", T.PRICING["models"])
        self.assertEqual(T.pricing_for([self.P])["models"], {"gpt-5.6-sol": T.ASSUMED_PRICE})
        self.assertIn("всем моделям Codex", T.PRICING["note"])

    def test_call_states(self):
        st = {c["call_id"]: c["state"] for c in self.P["calls"]}
        self.assertEqual(st, {"o1": "unknown", "w1": "success", "o2": "unknown", "o3": "error", "o4": "error",
                              "x1": "error", "a1": "success", "g1": "success"})
        self.assertEqual((self.S["calls"], self.S["errors"], self.S["unknown_results"]), (8, 3, 2))
        by = {c["call_id"]: c for c in self.P["calls"]}
        self.assertIn("cell 7", by["o1"]["note"])
        self.assertIn("js execution timed out", by["o4"]["note"])
        self.assertTrue(by["x1"]["note"].startswith("exit 2"))
        self.assertTrue(by["w1"]["is_wait"] and by["g1"]["is_wait"])
        self.assertEqual(T._call_title(by["w1"]), "wait · ожидание скрипта (cell 7)")

    def test_items_without_started_at(self):
        mcp = T.aggregate_mcp([self.P])
        self.assertEqual((mcp[0]["server"], mcp[0]["calls"], mcp[0]["errors"], mcp[0]["nested"]), ("node_repl", 1, 1, 1))
        # one standalone ContextCompaction item + one `compacted` record with its duplicate item = 2
        self.assertEqual(self.S["compactions"], 2)
        self.assertEqual([c["line"] for c in self.P["compactions"]], [29, 36])

    def test_commands_from_call_text(self):
        cmds = {(ce["norm"], ce["exit"]) for ce in self.P["ces"]}
        self.assertEqual(cmds, {("npm test", None), ("cat", None), ("sleep", None), ("git status", 2)})
        rows = {r["cmd"]: r for r in T.aggregate_commands([self.P], [self.S])}
        self.assertEqual((rows["git status"]["runs"], rows["git status"]["failed"]), (1, 1))
        self.assertEqual((rows["npm test"]["failed"], rows["npm test"]["unknown"]), (0, 1))

    def test_open_turn_ends_at_its_last_event(self):
        t0, t1 = self.P["turns"]
        self.assertEqual(t0["state"], "open")
        self.assertEqual(t0["dur_ms_eff"], 81000)             # 09:00:01 -> last AgentMessage 09:01:22, not 2 days
        self.assertEqual(self.TL["turns"][0]["dur_ms"], 81000)
        self.assertAlmostEqual(self.S["active_min"], (81000 + 60000) / 60000, delta=0.051)
        daily = {d["date"]: d for d in self.S["daily"]}
        self.assertAlmostEqual(daily["2026-02-01"]["agent_min"], 1.35, delta=0.051)
        self.assertEqual(daily["2026-02-03"]["agent_min"], 1.0)   # duration_ms, placed at the end of the turn
        self.assertAlmostEqual(sum(d["agent_min"] for d in self.S["daily"]), self.S["active_min"], delta=0.11)
        # the open turn's last event is the anchor before the next prompt: 2 days -> away, 0 minutes
        self.assertEqual(self.S["user_min"], 0.0)
        self.assertEqual(self.P["away_gaps"], 1)

    def test_end_and_gaps(self):
        self.assertEqual(self.S["end"], "2026-02-03T12:00:00.000Z")
        self.assertEqual(self.S["wall_min"], 3060.0)
        gaps = " ".join(T.session_gaps(self.P))
        self.assertIn("без task_complete", gaps)
        self.assertIn("длиннее duration_ms", gaps)
        self.assertIn("совпадает с итоговым total_token_usage с учётом 1", gaps)
        self.assertEqual(self.S["flags"], ["coldcache", "compact"])


class MixedTokenSources(unittest.TestCase):
    def test_no_double_counting(self):
        def rec(i, typ, pl, t="2026-03-01T10:00:%02d.000Z"):
            return json.dumps({"timestamp": t % i, "ordinal": i, "type": typ, "payload": pl})

        def usage(a, b, c, d):
            return {"input_tokens": a, "cached_input_tokens": b, "output_tokens": c, "reasoning_output_tokens": d,
                    "total_tokens": a + c}

        def tc(last, total):
            return {"type": "token_count", "info": {"last_token_usage": last, "total_token_usage": total}}

        lines = [rec(0, "session_meta", {"id": "00000000-0000-4000-8000-00000000abcd", "cwd": "/tmp/m"}),
                 rec(1, "event_msg", {"type": "task_started", "turn_id": "t"}),
                 rec(2, "event_msg", tc(usage(100, 0, 10, 0), usage(100, 0, 10, 0))),            # old part
                 rec(3, "token_usage_record", {"turn_id": "t", "usage": usage(200, 100, 20, 5)}),
                 rec(4, "event_msg", tc(usage(200, 100, 20, 5), usage(300, 100, 30, 5))),         # same response
                 rec(5, "token_usage_record", {"turn_id": "t", "usage": usage(50, 0, 5, 0)}),     # compaction call
                 rec(6, "event_msg", {"type": "task_complete", "turn_id": "t", "duration_ms": 5000})]
        tmp = Path(tempfile.mkdtemp())
        try:
            path = tmp / "rollout-2026-03-01T10-00-00-00000000-0000-4000-8000-00000000abcd.jsonl"
            path.write_text("\n".join(lines) + "\n")
            P = T.parse_rollout(path)
            S = T.session_core(P)
            self.assertEqual(S["reqs"], 3)
            self.assertEqual(S["tok"], {"input": 350, "cached": 100, "output": 35, "reasoning": 5})
            self.assertEqual((P["tok_stat"]["tur"], P["tok_stat"]["tc_used"], P["tok_stat"]["tc_matched"]), (2, 1, 1))
        finally:
            shutil.rmtree(tmp)


if __name__ == "__main__":
    unittest.main()
