"""Bundled local model (zero scored tokens).

A small GGUF (gemma-2-2b-it) baked into the image answers the task categories
where it is reliably correct — measured on the eval set: summarization and NER,
both extraction/transformation tasks a 2B handles well. Those answers cost zero
Fireworks tokens. Everything else, and any local failure, routes to Fireworks
unchanged, so the layer is strictly additive: it can only remove paid calls,
never break the run.
"""

from __future__ import annotations

import os
import sys
import threading
import time

_llm = None
# llama.cpp is not thread-safe; the worker pool must serialize access to it.
_lock = threading.Lock()
_CATEGORIES = {c.strip() for c in os.environ.get("LOCAL_CATEGORIES", "").split(",") if c.strip()}

# Measured generation speed (tokens/sec) on THIS box, set by a startup warmup.
# The grading box is CPU-only/2 vCPU and its speed is unknown until we run, so
# routing adapts to it: a task is only kept local if its estimated generation
# time fits the remaining local-time budget (see main.py) — otherwise it goes
# to Fireworks. This makes a slow box shed long-output work instead of timing
# out, and a fast box keep everything local.
_tok_s = 0.0
# Typical output length per category (tokens), from the local benchmark; used
# to estimate generation time before running. Deliberately a bit generous.
_TYPICAL_OUT = {
    "code_gen": 140, "code_debug": 100, "math": 240, "logic": 320,
    "sentiment": 30, "ner": 45, "summarization": 60, "factual": 45,
}


def _enabled() -> bool:
    return os.environ.get("LOCAL", "true").strip().lower() in ("1", "true", "yes")


def start() -> None:
    """Load the bundled model, or degrade to Fireworks-only on any problem.
    Must finish inside the container-start budget; a 1.6GB Q4 loads in seconds."""
    global _llm
    if not _enabled() or not _CATEGORIES:
        return
    path = os.environ.get("LOCAL_MODEL_PATH", "/models/model.gguf")
    try:
        if not os.path.exists(path) or os.path.getsize(path) == 0:
            print(f"local: no model at {path} — Fireworks-only", file=sys.stderr)
            return
    except OSError:
        return
    try:
        from llama_cpp import Llama
        _llm = Llama(
            model_path=path,
            n_ctx=int(os.environ.get("LOCAL_CTX_SIZE", "4096")),
            n_threads=int(os.environ.get("LOCAL_THREADS", "2")),  # 2 vCPU grading box
            verbose=False,
        )
        print(f"local: model loaded ({path}); categories={sorted(_CATEGORIES)}", file=sys.stderr)
    except Exception as exc:  # any load failure → Fireworks-only
        print(f"local: load failed ({exc}) — Fireworks-only", file=sys.stderr)
        _llm = None


def _measure_speed() -> None:
    """Time a short generation to learn this box's tokens/sec, so routing can
    predict per-task cost. Best-effort: on any failure speed stays 0 (unknown),
    which the caller treats as 'no time guard' and relies on the wall-clock
    budget instead."""
    global _tok_s
    try:
        t = time.monotonic()
        with _lock:
            resp = _llm.create_chat_completion(
                messages=[{"role": "user", "content": "List the numbers 1 to 40."}],
                max_tokens=64, temperature=0)
        dt = time.monotonic() - t
        out = (resp.get("usage", {}) or {}).get("completion_tokens", 0) or 0
        if dt > 0 and out > 0:
            _tok_s = out / dt
            print(f"local: warmup {out} tok in {dt:.1f}s -> {_tok_s:.1f} tok/s", file=sys.stderr)
    except Exception as exc:
        print(f"local: warmup failed ({exc}); no time guard", file=sys.stderr)


def tok_s() -> float:
    return _tok_s


def est_seconds(category: str) -> float:
    """Estimated local generation time for a category, or 0 if speed unknown
    (unknown → don't block on time, fall back to the wall-clock budget).
    A 1.3x margin guards against the warmup over-reading the box's speed."""
    if _tok_s <= 0:
        return 0.0
    return 1.3 * _TYPICAL_OUT.get(category, 200) / _tok_s


def available_for(category: str) -> bool:
    return _llm is not None and category in _CATEGORIES


# Remaining local-generation time budget (seconds), reserved greedily in task
# order so the box never commits to more local work than it can finish. None
# disables the guard (used in the non-batch path, which already runs in a
# deadline-bounded pool).
_budget_left = None
_budget_lock = threading.Lock()


def set_budget(seconds: float | None) -> None:
    global _budget_left
    _budget_left = seconds


def try_reserve(category: str) -> bool:
    """True if this task should run locally: the model is available for the
    category AND its estimated time fits the remaining budget (reserving it).
    A slow box thus keeps short tasks local and sheds long ones to Fireworks."""
    if _llm is None or category not in _CATEGORIES:
        return False
    global _budget_left
    with _budget_lock:
        if _budget_left is None:
            return True
        est = est_seconds(category)
        if est <= 0:            # unknown speed → rely on the wall-clock backstop
            return True
        if est <= _budget_left:
            _budget_left -= est
            return True
    return False


def complete(system: str, prompt: str, max_tokens: int) -> str:
    """One local completion. gemma-2 has no system role, so the instruction is
    folded into the user turn. Serialized: llama.cpp is not thread-safe.
    Returns '' on empty output (caller falls back to Fireworks)."""
    with _lock:
        resp = _llm.create_chat_completion(
            messages=[{"role": "user", "content": f"{system}\n\n{prompt}"}],
            max_tokens=max_tokens,
            temperature=0,
        )
    return (resp["choices"][0]["message"]["content"] or "").strip()


_CLASSIFY_LABELS = ("factual", "math", "sentiment", "summarization",
                    "ner", "code_debug", "code_gen", "logic")
_CLASSIFY_PROMPT = (
    "Classify this task into exactly ONE category. Reply with only the category "
    "name — nothing else.\n"
    "Categories: factual (knowledge questions), math (calculation/reasoning), "
    "sentiment (positive/negative/neutral), summarization, ner (extract "
    "entities), code_debug (fix broken code), code_gen (write new code), "
    "logic (deduction puzzles).\n\nTask:\n")


def classify_text(prompt: str) -> str:
    """Best-guess category label via the bundled model (zero Fireworks tokens),
    or '' if the model is unavailable or the reply is unusable. Used only as a
    semantic fallback when the regex classifier matched nothing."""
    if _llm is None:
        return ""
    try:
        with _lock:
            resp = _llm.create_chat_completion(
                messages=[{"role": "user",
                           "content": _CLASSIFY_PROMPT + (prompt or "")[:800]}],
                max_tokens=8, temperature=0)
        out = (resp["choices"][0]["message"]["content"] or "").strip().lower()
    except Exception:
        return ""
    import re as _re
    for lab in sorted(_CLASSIFY_LABELS, key=len, reverse=True):
        if out == lab or out.startswith(lab) or _re.search(rf"\b{lab}\b", out):
            return lab
    return ""
