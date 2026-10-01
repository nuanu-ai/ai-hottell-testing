"""Тесты выводов hottell UI v3 на синтетических данных (testdata/conclusions — выдуманное).

Запуск: cd ui/builder && python3 -m unittest test_conclusions -v
"""

import os
import unittest

import conclusions as c

HERE = os.path.dirname(os.path.abspath(__file__))
DATA = os.path.join(HERE, "testdata", "conclusions")
REPORTS = os.path.join(DATA, "reports")
TESTS = os.path.join(DATA, "tests")
A = "00000000-0000-4000-8000-00000000000a"
B = "00000000-0000-4000-8000-00000000000b"
OTHER = "00000000-0000-4000-8000-0000000000ff"


def by_source(result, source):
    return [f for f in result["findings"] if f["source"] == source]


class SeverityTest(unittest.TestCase):
    def test_only_confirmation_makes_bad(self):
        confirmed = [{"pattern": "D12_scope", "status": "confirmed"}]
        self.assertEqual(c._severity("D12", confirmed, [], "low", []), "bad")
        self.assertEqual(c._severity("D12", [], [{"id": "D12", "status": "confirmed"}], None, []), "bad")
        # Высокий приоритет без подтверждения — важная гипотеза, не подтверждённая проблема.
        self.assertEqual(c._severity("D12", [], [], "high", []), "warn")
        self.assertEqual(c._severity("D12", [], [{"id": "D12", "status": "suspected"}], None, []), "warn")
        self.assertEqual(c._severity(None, [], [], "low", []), "info")


class CatalogueTest(unittest.TestCase):
    def test_thirteen_checks_in_order(self):
        cat = c.load_catalogue()
        self.assertEqual([x["id"] for x in cat], list(c.CHECK_IDS))
        for item in cat:
            self.assertEqual(set(item), {"id", "name", "how"})
            self.assertRegex(item["name"] + item["how"], "[А-Яа-я]")


class EvidenceRefTest(unittest.TestCase):
    def test_line_forms(self):
        self.assertEqual(c.normalize_ref("local_transcript:L1273"), (None, 1273))
        self.assertEqual(c.normalize_ref("L1273"), (None, 1273))
        self.assertEqual(c.normalize_ref("#1273"), (None, 1273))
        self.assertEqual(c.normalize_ref(f"{A}:L12"), (A, 12))

    def test_range_takes_first_line(self):
        self.assertEqual(c.normalize_ref("L1-L5859"), (None, 1))
        self.assertEqual(c.normalize_ref(f"{A}:L10-L20"), (A, 10))
        self.assertEqual(c.normalize_ref("L1-L5859: token_count scan"), (None, 1))

    def test_review_event_is_ordinal(self):
        self.assertEqual(c.normalize_ref({"kind": "event", "id": "1218"}), (None, 1219))
        self.assertEqual(c.normalize_ref({"kind": "event", "id": "0"}), (None, 1))

    def test_rejects_garbage(self):
        for ref in ("L0", "garbage", "", None, True, 0, {"kind": "call", "id": "x"}):
            self.assertEqual(c.normalize_ref(ref), (None, None), ref)

    def test_ref_lines_drops_other_sessions(self):
        refs = [f"{A}:L12", f"{OTHER}:L3", "L7", "#7", "L1-L5"]
        self.assertEqual(c.ref_lines(refs, A), [1, 7, 12])


class V2SessionTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.r = c.load_session(A, REPORTS, TESTS)
        cls.v2 = {f["id"]: f for f in by_source(cls.r, "deep-v2")}

    def test_session_fields(self):
        sf = self.r["session_fields"]
        self.assertEqual(sf["sources"]["deep"], "v2")
        self.assertTrue(sf["sources"]["test_review"])
        self.assertEqual(sf["sources"]["checks_from"], "deep-v2")
        self.assertEqual(sf["corrections"], 2)
        self.assertEqual(sf["title"], "Составить недельный заказ вымышленной пекарни.")
        # Итог сессии — из v1, с явной пометкой; v2 даёт только исходы заданий.
        self.assertEqual(sf["outcome"], "partial")
        self.assertIn("Итог сессии — из Deep v1", sf["outcome_basis"])
        self.assertIn("подтверждено 1, частично 1", sf["outcome_basis"])
        self.assertEqual([t["task_id"] for t in sf["tasks"]], ["T01", "T02"])
        self.assertEqual(len(sf["checks"]), 13)
        d07 = next(x for x in sf["checks"] if x["id"] == "D07")
        self.assertEqual(d07["lines"], [12])  # ссылка на чужую сессию отброшена
        d01 = next(x for x in sf["checks"] if x["id"] == "D01")
        self.assertEqual(d01["lines"], [7, 9])
        self.assertEqual(self.r["human_turn_classes"], {0: "task", 1: "correction", 2: "correction", 3: "answer"})

    def test_v2_proposal_maps_to_finding(self):
        f = self.v2["p2:fixture0000000000001"]
        self.assertEqual(f["title"], "Добавить в скрипт заказа проверку границы поставщиков")
        self.assertTrue(f["what"].startswith("Листы поставщиков дважды объединились"))
        self.assertIn("Вероятная причина: скрипт заказа проверяет суммы", f["what"])
        self.assertEqual(f["where"], "/tmp/fixture/bakery/order_check.py")
        self.assertEqual(f["kind"], "script")  # workflow → script
        self.assertEqual(f["change_type"], "workflow")
        self.assertEqual(f["pattern_id"], "D12")
        self.assertEqual(f["readiness"], "needs_spec")
        self.assertEqual((f["decision"], f["execution"], f["effect"]), ("not_requested", "not_applied", "not_measured"))
        self.assertEqual(f["sev"], "bad")  # наблюдение D12_* подтверждено
        self.assertEqual(f["sessions"], [A])
        for e in f["ev"]:
            self.assertEqual(set(e) >= {"sid", "line", "at", "text"}, True)
            self.assertIsNone(e["at"])

    def test_duplicate_proposal_is_merged(self):
        ids = [f["id"] for f in self.r["findings"]]
        self.assertEqual(ids.count("p2:fixture0000000000001"), 1)
        lines = [e["line"] for e in self.v2["p2:fixture0000000000001"]["ev"]]
        self.assertIn(70, lines)  # основание дубля добавлено к одной карточке

    def test_episode_attaches_to_matching_pattern(self):
        f = self.v2["p2:fixture0000000000001"]
        by_line = {e["line"]: e for e in f["ev"]}
        self.assertEqual(sorted(by_line), [50, 60, 61, 70, 80])
        self.assertIn("test-review", by_line[50]["src"])
        self.assertIn("deep-v2", by_line[50]["src"])
        self.assertEqual(by_line[80]["text"], "Эпизод D12: Человек снова просит разделить")
        self.assertEqual(f["impact"], {"value": "2 коррекции", "label": "в эпизоде D12, реплики 1–2 (разметка тестового разбора)"})
        d12 = next(e for e in self.r["episodes"] if e["pattern_id"] == "D12")
        self.assertEqual(d12["lines"], [50, 80])
        self.assertEqual(d12["attached_to"], ["p2:fixture0000000000001"])

    def test_episode_alone_creates_no_card(self):
        self.assertFalse([f for f in self.r["findings"] if f["pattern_id"] == "D01"])
        d01 = next(e for e in self.r["episodes"] if e["pattern_id"] == "D01")
        self.assertEqual(d01["lines"], [7])
        self.assertEqual(d01["attached_to"], [])
        self.assertEqual(len(self.r["findings"]), 4)

    def test_review_proposal_duplicate_adds_evidence_only(self):
        self.assertEqual(len(by_source(self.r, "test-review")), 1)  # D12 уже есть в Deep v2
        f = self.v2["p2:fixture0000000000001"]
        self.assertIn("test-review", {e["line"]: e for e in f["ev"]}[50]["src"])

    def test_review_proposal_becomes_hypothesis_card(self):
        [f] = by_source(self.r, "test-review")
        self.assertEqual(f["pattern_id"], "D16")
        self.assertEqual(f["kind"], "skill")
        self.assertEqual(f["readiness"], "hypothesis")
        self.assertEqual(f["title"], "Перед словом «готово» сверить остатки склада с заказом")
        self.assertEqual(f["where"], "skill подготовки недельного заказа")
        self.assertEqual([e["line"] for e in f["ev"]], [30, 40])
        self.assertTrue(f["what"].startswith("Было: заказ объявлен готовым"))
        self.assertIn("метрика early_done_share, ожидается ниже", f["expected_effect"])

    def test_unconfirmed_low_priority_is_info_without_where(self):
        f = self.v2["p2:fixture0000000000002"]
        self.assertEqual(f["sev"], "info")
        self.assertIsNone(f["where"])
        self.assertIsNone(f["cause"])
        self.assertEqual(f["readiness"], "hypothesis")
        self.assertEqual(f["kind"], "diagnostic")
        self.assertIsNone(f["impact"])

    def test_v1_draft_superseded_by_v2_and_deduped(self):
        f = self.v2["p2:fixture0000000000001"]
        self.assertEqual([x["title"] for x in f["replaces"]], ["Проверять раздельные листы поставщиков"])
        drafts = by_source(self.r, "deep-v1")
        self.assertEqual([d["title"] for d in drafts], ["Записывать дату поставки"])
        self.assertEqual(sorted(e["line"] for e in drafts[0]["ev"]), [20, 21])

    def test_translation_fallback(self):
        f = self.v2["p2:fixture0000000000001"]
        self.assertEqual(f["rollback"], "Restore the previous order script version.")  # перевода нет
        self.assertEqual(f["lang"], "en")
        t01 = self.r["session_fields"]["tasks"][0]
        self.assertEqual((t01["goal"], t01["lang"]), ("Составить недельный список заказа пекарни.", "ru-translated"))
        t02 = self.r["session_fields"]["tasks"][1]
        self.assertEqual((t02["goal"], t02["lang"]), ("Split flour and sugar suppliers into separate sheets.", "en"))
        self.assertTrue(any("Без перевода" in g for g in self.r["gaps"]))

    def test_full_translation_marks_ru_translated(self):
        mapping = {}
        _, tr = c._load(A, REPORTS, TESTS, translations={})
        for text in tr.missing:
            mapping[c.text_key(text)] = "перевод: " + text
        r, tr2 = c._load(A, REPORTS, TESTS, translations=mapping)
        self.assertEqual(tr2.missing, [])
        f = next(x for x in r["findings"] if x["id"] == "p2:fixture0000000000001")
        self.assertEqual(f["lang"], "ru-translated")
        self.assertEqual(f["where"], "/tmp/fixture/bakery/order_check.py")  # путь не переводится
        self.assertEqual(r["gaps"], [])


class V1SessionTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.r = c.load_session(B, REPORTS, TESTS)

    def test_v1_draft_finding(self):
        sf = self.r["session_fields"]
        self.assertEqual(sf["sources"], {"deep": "v1", "coverage": True, "test_review": False,
                                         "checks_from": "coverage", "outcome_from": "deep-v1"})
        self.assertIsNone(sf["corrections"])
        self.assertEqual(sf["tasks"], [])
        self.assertEqual(self.r["human_turn_classes"], {})
        self.assertEqual(self.r["episodes"], [])
        (f,) = self.r["findings"]
        self.assertEqual(f["source"], "deep-v1")
        self.assertEqual((f["sev"], f["readiness"]), ("info", "hypothesis"))
        self.assertEqual((f["decision"], f["execution"], f["effect"]), ("not_requested", "not_applied", "not_measured"))
        self.assertEqual(f["kind"], "project_rule")  # объект правки — AGENTS.md
        self.assertEqual(f["where"], "AGENTS.md проекта покупок")
        self.assertEqual(f["snip"], "Сверять список с чеком перед ответом.")
        self.assertIsNone(f["impact"])
        self.assertIsNone(f["cause"])
        self.assertIn("«Сверка списка»", f["what"])
        self.assertEqual(f["lang"], "ru")
        self.assertEqual(f["ev"][0]["line"], 8)

    def test_coverage_checks_filled_to_thirteen(self):
        checks = self.r["session_fields"]["checks"]
        self.assertEqual([x["id"] for x in checks], list(c.CHECK_IDS))
        d05 = next(x for x in checks if x["id"] == "D05")
        self.assertEqual((d05["summary"], d05["lang"]), ("Последний ход не заявляет о готовности.", "ru-translated"))
        d01 = next(x for x in checks if x["id"] == "D01")
        self.assertEqual(d01["lines"], [1])
        d03 = next(x for x in checks if x["id"] == "D03")
        self.assertIsNone(d03["status"])  # нет в источнике — «нет данных», не выдуманный статус


class MissingSessionTest(unittest.TestCase):
    def test_nothing_available(self):
        r = c.load_session("00000000-0000-4000-8000-000000000000", REPORTS, TESTS)
        sf = r["session_fields"]
        self.assertEqual(sf["sources"]["deep"], "none")
        self.assertEqual((sf["title"], sf["outcome"], sf["deep"], sf["corrections"]), (None, None, None, None))
        self.assertEqual((sf["tasks"], sf["checks"], r["findings"], r["episodes"]), ([], [], [], []))


if __name__ == "__main__":
    unittest.main()


# ---------------------------------------------------------------- load_all

REG = os.path.join(HERE, "testdata", "conclusions_registry")
REG_REPORTS = os.path.join(REG, "reports")
REG_TESTS = os.path.join(REG, "tests")
C = "00000000-0000-4000-8000-00000000000c"
FOREIGN = "00000000-0000-4000-8000-0000000000ff"


class LoadAllRegistryTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.r = c.load_all([A, C], REG_REPORTS, REG_TESTS, translations={})
        cls.by_id = {f["id"]: f for f in cls.r["findings"]}

    def test_registry_replaces_candidates(self):
        deep = [f for f in self.r["findings"] if f["source"] == "deep-v2"]
        self.assertEqual(sorted(f["id"] for f in deep), ["p2:reg-c", "p2:reg-multi"])
        self.assertTrue(all(f.get("registry") for f in deep))
        self.assertNotIn("p2:shared", self.by_id)          # кандидаты сессий карточек не дают
        self.assertNotIn("p2:cand-a-only", self.by_id)
        self.assertNotIn("p2:reg-foreign", self.by_id)     # ни одного источника из выборки
        for sid in (A, C):
            self.assertEqual(self.r["sessions"][sid]["session_fields"]["sources"]["findings_from"], "registry")

    def test_multi_session_card(self):
        f = self.by_id["p2:reg-multi"]
        self.assertEqual(f["sessions"], [A, C, FOREIGN])     # чужая сессия сохраняется
        self.assertEqual([(e["sid"], e["line"]) for e in f["ev"]],
                         [(A, 50), (A, 60), (A, 70), (C, 7), (FOREIGN, 3)])
        self.assertTrue(f["ev"][3]["text"].startswith("Deep v2 · T01: Пересчитать выдуманную полку"))
        self.assertEqual(f["ev"][4]["text"], "Deep v2 · T09")  # задание чужой сессии неизвестно
        self.assertEqual(f["sev"], "bad")                     # наблюдение D12_* в сессии A подтверждено
        self.assertEqual(f["impact"]["value"], "1 коррекция")
        self.assertEqual(self.by_id["p2:reg-c"]["sev"], "info")
        self.assertEqual(self.by_id["p2:reg-c"]["readiness"], "hypothesis")
        ids_a = {x["id"] for x in self.r["sessions"][A]["findings"]}
        ids_c = {x["id"] for x in self.r["sessions"][C]["findings"]}
        self.assertIn("p2:reg-multi", ids_a & ids_c)
        self.assertNotIn("p2:reg-c", ids_a)

    def test_v1_hidden_with_registry(self):
        self.assertFalse([f for f in self.r["findings"] if f["source"] == "deep-v1"])
        self.assertEqual(sum("Черновики рекомендаций Deep v1 не показаны" in g for g in self.r["gaps"]), 1)

    def test_test_review_and_episode_attach_to_registry_card(self):
        f = self.by_id["p2:reg-multi"]
        by = {(e["sid"], e["line"]): e for e in f["ev"]}
        self.assertEqual(by[(A, 60)]["text"], "Эпизод D12: Склады снова объединены")
        self.assertIn("test-review", by[(A, 70)]["src"])      # предложение тестового разбора → основание
        episode = self.r["sessions"][A]["episodes"][0]
        self.assertEqual(episode["attached_to"], ["p2:reg-multi"])
        [own] = [x for x in self.r["findings"] if x["source"] == "test-review"]
        self.assertEqual((own["pattern_id"], own["sessions"]), ("D01", [A]))   # своей карточки D01 нет

    def test_only_foreign_selection_keeps_foreign_ids(self):
        r = c.load_all([C], REG_REPORTS, REG_TESTS, translations={})
        f = next(x for x in r["findings"] if x["id"] == "p2:reg-multi")
        self.assertEqual(f["sessions"], [A, C, FOREIGN])
        self.assertEqual(f["ev"][0]["text"], "Deep v2 · T02")  # сессия A не загружена
        self.assertIsNone(f["impact"])                          # эпизоды A не прикреплены
        self.assertEqual(r["gaps"], [])                         # у C нет черновиков v1

    def test_load_session_unchanged(self):
        r = c.load_session(A, REG_REPORTS, REG_TESTS)
        ids = sorted(f["id"] for f in r["findings"])
        self.assertIn("p2:shared", ids)                         # без реестра — кандидаты сессии
        self.assertNotIn("findings_from", r["session_fields"]["sources"])


class LoadAllFallbackTest(unittest.TestCase):
    def setUp(self):
        import shutil
        import tempfile
        self.tmp = tempfile.mkdtemp()
        self.reports = os.path.join(self.tmp, "reports")
        shutil.copytree(REG_REPORTS, self.reports)
        os.remove(os.path.join(self.reports, "v2", "proposals.json"))
        self.addCleanup(shutil.rmtree, self.tmp)

    def test_merge_across_sessions_without_registry(self):
        r = c.load_all([A, C], self.reports, REG_TESTS, translations={})
        by_id = {f["id"]: f for f in r["findings"]}
        self.assertEqual(sorted(by_id), sorted(["p2:shared", "p2:cand-a-only", next(k for k in by_id if k.startswith("v1:")),
                                                next(k for k in by_id if k.startswith("tr:"))]))
        f = by_id["p2:shared"]
        self.assertEqual(f["sessions"], [A, C])
        self.assertEqual([(e["sid"], e["line"]) for e in f["ev"]], [(A, 50), (A, 60), (A, 70), (C, 7)])
        self.assertEqual(f["sev"], "bad")
        self.assertEqual(f["readiness"], "hypothesis")          # наименьшая готовность из двух сессий
        self.assertEqual(by_id[next(k for k in by_id if k.startswith("v1:"))]["title"], "Черновик v1 про поставки")
        self.assertEqual(r["sessions"][A]["session_fields"]["sources"]["findings_from"], "session")
        self.assertEqual(r["gaps"], [])

    def test_invalid_registry_falls_back(self):
        with open(os.path.join(self.reports, "v2", "proposals.json"), "w", encoding="utf-8") as fh:
            fh.write('{"kind": "something_else", "schema_version": 2, "proposals": []}')
        r = c.load_all([A, C], self.reports, REG_TESTS, translations={})
        self.assertIn("p2:shared", {f["id"] for f in r["findings"]})
        self.assertTrue(any("Реестр предложений не соответствует схеме" in g for g in r["gaps"]))
