import copy
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from analytics.v2_skills import validate_skill_report
from local.v2_skills_report import metadata, snapshot, verify_inventory_current


class SkillReportContractTest(unittest.TestCase):
    def setUp(self):
        self.deep = {
            "s1": {"source_sha256": "a" * 64, "tasks": [{"task_id": "T1", "start_line": 2, "end_line": 5}]},
            "s2": {"source_sha256": "b" * 64, "tasks": [{"task_id": "T2", "start_line": 7, "end_line": 9}]},
        }
        self.report = {
            "kind": "skill_opportunities", "schema_version": 1, "analyzed_at": "2026-09-30T00:00:00Z",
            "inventory": {"snapshot_at": "2026-09-30T00:00:00Z", "items": [{"name": "playwright", "description": "browser", "path_class": "~/.codex/skills/playwright", "description_sha256": "c" * 64}]},
            "corpus": [{"session_id": "s1", "source_sha256": "a" * 64, "deep_report_sha256": "d" * 64}, {"session_id": "s2", "source_sha256": "b" * 64, "deep_report_sha256": "e" * 64}],
            "opportunities": [{"id": "release-gate", "kind": "create", "title": "Release gate", "recommendation": "Create", "why_skill": "Judgment", "alternative": "Script for exact checks", "uncertainty": "Existing runbook unknown", "verification": "Compare next release", "readiness": "hypothesis", "copy_prompt": "Use skill-creator", "sources": [
                {"session_id": "s1", "task_id": "T1", "source_sha256": "a" * 64, "evidence": ["L2"]},
                {"session_id": "s2", "task_id": "T2", "source_sha256": "b" * 64, "evidence": ["L8"]},
            ]}],
        }

    def test_valid_cross_session_skill(self):
        validate_skill_report(self.report, self.deep)

    def test_does_not_promote_single_session_to_custom_skill(self):
        report = copy.deepcopy(self.report)
        report["opportunities"][0]["sources"] = report["opportunities"][0]["sources"][:1]
        with self.assertRaisesRegex(ValueError, "cross-session"):
            validate_skill_report(report, self.deep)

    def test_rejects_out_of_task_evidence_and_stale_source(self):
        report = copy.deepcopy(self.report)
        report["opportunities"][0]["sources"][0]["evidence"] = ["L6"]
        with self.assertRaisesRegex(ValueError, "outside task"):
            validate_skill_report(report, self.deep)
        report = copy.deepcopy(self.report)
        report["corpus"][0]["source_sha256"] = "d" * 64
        with self.assertRaisesRegex(ValueError, "version mismatch"):
            validate_skill_report(report, self.deep)

    def test_requires_deep_report_version(self):
        report = copy.deepcopy(self.report)
        del report["corpus"][0]["deep_report_sha256"]
        with self.assertRaisesRegex(ValueError, "Deep report hash required"):
            validate_skill_report(report, self.deep)

    def test_current_skill_is_not_a_historical_miss(self):
        report = copy.deepcopy(self.report)
        opportunity = report["opportunities"][0]
        opportunity["kind"] = "use_existing"
        opportunity["installed_skill"] = "playwright"
        opportunity["historical_availability"] = "known"
        opportunity.pop("copy_prompt")
        with self.assertRaisesRegex(ValueError, "historical availability"):
            validate_skill_report(report, self.deep)

    def test_inventory_cannot_be_invented_by_analyst(self):
        report = copy.deepcopy(self.report)
        report["inventory"]["items"][0]["name"] = "invented"
        with self.assertRaisesRegex(ValueError, "recorded local snapshot"):
            validate_skill_report(report, self.deep, self.report["inventory"])

    def test_publication_rejects_changed_installed_skills(self):
        recorded = {"scope": "local", "items": [{"name": "playwright", "description_sha256": "a" * 64}]}
        current = copy.deepcopy(recorded)
        verify_inventory_current(recorded, current)
        current["items"][0]["description_sha256"] = "b" * 64
        with self.assertRaisesRegex(ValueError, "changed since analysis"):
            verify_inventory_current(recorded, current)

    def test_snapshot_excludes_runtime_system_skills(self):
        with tempfile.TemporaryDirectory() as folder:
            home = Path(folder)
            for name, relative in (("playwright", ".codex/skills/playwright"),
                                   ("review-agent", ".codex/skills/.system/review-agent")):
                path = home / relative / "SKILL.md"
                path.parent.mkdir(parents=True)
                path.write_text(f"---\nname: {name}\ndescription: Current capability\n---\n", encoding="utf-8")
            with patch.object(Path, "home", return_value=home):
                result = snapshot()
        self.assertEqual([item["name"] for item in result["items"]], ["playwright"])

    def test_multiline_skill_description_is_read_as_text(self):
        with tempfile.TemporaryDirectory(dir=Path.home()) as folder:
            path = Path(folder) / "SKILL.md"
            path.write_text("---\nname: example\ndescription: |\n  First line\n  second line\n---\nbody\n")
            item = metadata(path)
        self.assertEqual(item["description"], "First line second line")


if __name__ == "__main__":
    unittest.main()
