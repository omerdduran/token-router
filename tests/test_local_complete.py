"""local.complete(): LocalOut semantics with a stubbed _llm — no GGUF, no
network. Pins the two behaviors main.py's escalation logic depends on:
truncated is True only when the deadline break fires, and the NamedTuple is
always truthy (callers must branch on out.text)."""

import os
import sys
import time
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

import local
from local import LocalOut


class _FakeLLM:
    """Yields OpenAI-style stream chunks, optionally sleeping between them so
    a deadline in the near future can expire mid-stream."""

    def __init__(self, pieces, delay=0.0):
        self.pieces = pieces
        self.delay = delay

    def create_chat_completion(self, **kwargs):
        assert kwargs.get("stream") is True
        def gen():
            yield {"choices": [{"delta": {"role": "assistant"}}]}
            for p in self.pieces:
                if self.delay:
                    time.sleep(self.delay)
                yield {"choices": [{"delta": {"content": p}}]}
        return gen()


class TestComplete(unittest.TestCase):
    def tearDown(self):
        local._llm = None

    def test_returns_localout_text_not_truncated(self):
        local._llm = _FakeLLM(["Hello", " world"])
        out = local.complete("sys", "prompt", 64)
        self.assertIsInstance(out, LocalOut)
        self.assertEqual(out.text, "Hello world")
        self.assertFalse(out.truncated)

    def test_no_deadline_never_truncates(self):
        local._llm = _FakeLLM(["a"] * 5)
        out = local.complete("sys", "prompt", 64, deadline=None)
        self.assertFalse(out.truncated)

    def test_past_deadline_truncates_and_sets_flag(self):
        local._llm = _FakeLLM(["one", "two", "three"], delay=0.03)
        out = local.complete("sys", "prompt", 64,
                             deadline=time.monotonic() + 0.04)
        self.assertTrue(out.truncated)
        self.assertLess(len(out.text), len("onetwothree"))

    def test_blank_stream_gives_empty_text(self):
        local._llm = _FakeLLM([])
        out = local.complete("sys", "prompt", 64)
        self.assertEqual(out.text, "")
        self.assertFalse(out.truncated)

    def test_localout_is_always_truthy(self):
        # The pitfall main.py must never rely on: bool(out) is useless.
        self.assertTrue(bool(LocalOut("", False)))
        self.assertFalse(bool(LocalOut("", False).text))


if __name__ == "__main__":
    unittest.main()
