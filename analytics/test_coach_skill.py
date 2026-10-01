"""Структура analytics/coach/SKILL.md: frontmatter, правила дословно из спеки, тулы MCP.

Run: python3 -m unittest analytics.test_coach_skill -v   (из корня репозитория)
"""

import re
import unittest
from pathlib import Path

SKILL = Path(__file__).resolve().parent / "coach" / "SKILL.md"

# docs/specs/2026-10-01-hottell-coach.spec.md, «Правила (в SKILL.md дословно)»
RULES = [
    "Не дублировать. Если правило уже есть в настройках, но нарушается, уточнять существующее (файл и строка), а не добавлять второе.",
    "Сначала поправить существующий skill, потом создавать новый. Имя нового — по классу задач, а не по сегодняшней задаче.",
    "Не превращать в правило: сбои окружения (это починка), утверждения «инструмент X не работает», разовые ошибки, разовые задачи, способы, которые так и не сработали.",
    "Предпочтение, относящееся к задаче, у которой есть skill, — в этот skill; общее — в инструкции. Никогда в оба места.",
    "Без цитат из сессий темы нет.",
    "Без явного согласия человека ничего не менять.",
    "Секреты, пути клиентов и имена третьих лиц маскируются везде: в цитатах, в изменениях и в журнале. В журнал попадают ссылки на события и цитата не длиннее 200 символов.",
]


class CoachSkillTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = SKILL.read_text(encoding="utf-8")

    def test_frontmatter(self):
        m = re.match(r"^---\nname: hottell-coach\ndescription: (.+)\n---\n", self.text)
        self.assertIsNotNone(m, "frontmatter: name: hottell-coach и однострочный description")
        for trigger in ("$hottell-coach", "/hottell-coach", "дашборд"):
            self.assertIn(trigger, m.group(1))

    def test_rules_verbatim(self):
        for rule in RULES:
            self.assertIn(rule, self.text)

    def test_data_contract(self):
        for word in ("hottell-local", "coach_journal", "findings", "undated", "session_read", "session_stats", "`from`",
                     "next_offset", "coverage", "line_of", "check_of", "checked_at", "repeats", "весь период", "сессия <id>", "HOTTELL_DATA_DIR"):
            self.assertIn(word, self.text)

    def test_completeness_over_pages_is_approximate(self):
        data = self.text.split("## Данные", 1)[1].split("## Ход разговора", 1)[0]
        self.assertIn("не снимок", data)
        self.assertIn("«≈»", data)

    def test_check_record_fields(self):
        journal = self.text.split("### 7. Журнал", 1)[1].split("## Правила", 1)[0]
        self.assertIn("Запись проверки содержит только `check_of`, `topic_key`, `agent`, `result`, `observations`, `repeats`, "
                      "`evidence`; поля решения (`decision`, `layer`, `topic`, `findings`, `target`, `change`, `rollback`, "
                      "`check`, `check_after`, sha) журнал отклонит.", journal)
        self.assertNotIn("не нужен.", journal)

    def test_data_dir_under_codex(self):
        data = self.text.split("## Данные", 1)[1].split("## Ход разговора", 1)[0]
        self.assertIn("[mcp_servers.hottell-local.env]", data)

    def test_findings_keep_only_newest_evidence(self):
        data = self.text.split("## Данные", 1)[1].split("## Ход разговора", 1)[0]
        self.assertIn("только самые новые доказательства", data)

    def test_layers_and_outcomes(self):
        for word in ("experience", "instructions", "skill", "technical", "applied", "declined", "not_justified",
                     "test", "repeated", "not_repeated", "not_enough_data"):
            self.assertIn(f"`{word}`", self.text)


if __name__ == "__main__":
    unittest.main()
