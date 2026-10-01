import unittest

from rebuild_telemetry import observed_usage


class ObservedUsageTest(unittest.TestCase):
    def test_duplicate_polls_do_not_inflate_totals(self):
        snapshots = [
            {"input_tokens": 8, "cached_input_tokens": 2, "output_tokens": 2, "total_tokens": 10},
            {"input_tokens": 8, "cached_input_tokens": 2, "output_tokens": 2, "total_tokens": 10},
            {"input_tokens": 20, "cached_input_tokens": 6, "output_tokens": 5, "total_tokens": 25},
            {"input_tokens": 4, "cached_input_tokens": 1, "output_tokens": 1, "total_tokens": 5},
        ]
        usage, valid, duplicates, resets = observed_usage(snapshots)
        self.assertEqual(usage["total_tokens"], 30)
        self.assertEqual(usage["input_tokens"], 24)
        self.assertEqual((valid, duplicates, resets), (4, 1, 1))

    def test_missing_counter_stays_unknown(self):
        self.assertEqual(observed_usage([None, {}]), (None, 0, 0, 0))


if __name__ == "__main__":
    unittest.main()
