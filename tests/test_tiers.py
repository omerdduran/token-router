import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

from llm import _select_tiers

# The five models the judging harness actually injects (they 404 on personal
# keys, so this is the only way to test tier selection against them).
HARNESS = [
    "accounts/fireworks/models/minimax-m3",
    "accounts/fireworks/models/kimi-k2p7-code",
    "accounts/fireworks/models/gemma-4-31b-it",
    "accounts/fireworks/models/gemma-4-26b-a4b-it",
    "accounts/fireworks/models/gemma-4-31b-it-nvfp4",
]


class TestTierSelection(unittest.TestCase):
    def test_harness_list(self):
        t = _select_tiers(HARNESS)
        # strong must NOT be the reasoning model minimax-m3 (which burns
        # scored completion tokens); it should be the dense 31b gemma.
        self.assertEqual(t["strong"], "accounts/fireworks/models/gemma-4-31b-it")
        self.assertEqual(t["cheap"], "accounts/fireworks/models/gemma-4-26b-a4b-it")
        self.assertEqual(t["code"], "accounts/fireworks/models/kimi-k2p7-code")
        self.assertNotIn("minimax", " ".join(t.values()))

    def test_prefers_unquantized_on_size_tie(self):
        t = _select_tiers([
            "accounts/fireworks/models/gemma-4-31b-it",
            "accounts/fireworks/models/gemma-4-31b-it-nvfp4",
        ])
        self.assertEqual(t["strong"], "accounts/fireworks/models/gemma-4-31b-it")

    def test_reasoning_only_list_falls_back(self):
        # If nothing but reasoning models is allowed, we must still pick one.
        t = _select_tiers(["accounts/fireworks/models/minimax-m3"])
        self.assertEqual(t["strong"], "accounts/fireworks/models/minimax-m3")


if __name__ == "__main__":
    unittest.main()
