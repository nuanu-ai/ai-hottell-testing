"""Contract checks for the private semantic eval runner; no case text in Git."""

import copy
import json
import tempfile
import unittest
from pathlib import Path

from analytics.v2_semantic_eval import REQUIRED_CASE_IDS, build_prompt, evaluate, load_cases


def private_case_stubs():
    return [
        {"id": case_id, "input": "Synthetic case text", "questions": {"answer": "Reply boolean"}, "expect": {"answer": True}}
        for case_id in sorted(REQUIRED_CASE_IDS)
    ]


class SemanticEvalTest(unittest.TestCase):
    def test_answers_are_withheld_from_model_and_scored(self):
        cases = private_case_stubs()
        prompt = build_prompt(cases)
        self.assertNotIn('"expect"', prompt)
        self.assertIn('"questions"', prompt)
        response = {"cases": [{"id": case["id"], "answers": copy.deepcopy(case["expect"])} for case in cases]}
        self.assertEqual(evaluate(cases, response), [])
        response["cases"][0]["answers"]["answer"] = False
        self.assertIn("expected True, got False", evaluate(cases, response)[0])

    def test_exact_case_set_and_answer_types_required(self):
        cases = private_case_stubs()
        response = {"cases": [{"id": case["id"], "answers": {"answer": True}} for case in cases]}
        response["cases"].pop()
        self.assertIn("exactly one", evaluate(cases, response)[0])
        response["cases"].append({"id": cases[-1]["id"], "answers": {"answer": 1}})
        self.assertIn("expected True, got 1", evaluate(cases, response)[0])
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "cases.json"
            path.write_text(json.dumps({"schema_version": 1, "cases": cases[:-1]}))
            with self.assertRaisesRegex(ValueError, "exactly six"):
                load_cases(path)


if __name__ == "__main__":
    unittest.main()
