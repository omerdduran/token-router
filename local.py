"""Bundled local model (zero scored tokens).

A small GGUF (gemma-4-E2B-it) baked into the image answers the categories in
LOCAL_CATEGORIES at zero Fireworks tokens. Any local failure (blank output,
exception, missed deadline) routes to Fireworks unchanged, so the layer is
strictly additive: it can only remove paid calls, never break the run.
"""

from __future__ import annotations

import os
import sys
import threading
import time
from typing import NamedTuple


class LocalOut(NamedTuple):
    """One local completion. text='' means blank. truncated=True ONLY when the
    hard-deadline break cut the stream — hitting max_tokens is normal output,
    the caps are tuned for it. PITFALL: a NamedTuple is always truthy; callers
    must branch on out.text, never on out itself."""
    text: str
    truncated: bool


_llm = None
# llama.cpp is not thread-safe; the worker pool must serialize access to it.
_lock = threading.Lock()
_CATEGORIES = {c.strip() for c in os.environ.get("LOCAL_CATEGORIES", "").split(",") if c.strip()}

# Measured speeds (tokens/sec) on THIS box: decode from the startup warmup,
# prefill learned online from each call's time-to-first-token. The grading box
# is CPU-only/2 vCPU and its speed is unknown until we run, so routing adapts:
# a task is only started locally if its estimated time fits before the hard
# stop (see main.py) — otherwise it goes to Fireworks. A slow box thus sheds
# long-output work instead of timing out; a fast box keeps everything local.
_tok_s = 0.0
_prefill_tok_s = 0.0
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
        return
    # Learn this box's decode speed up front: est_seconds() needs it to decide
    # which tasks fit before the hard stop. Also doubles as a model sanity check.
    _measure_speed()


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


def est_seconds(category: str, prompt_chars: int = 0) -> float:
    """Estimated local time for a task: decode (typical output length at the
    measured decode speed, with a 1.3x margin against the warmup over-reading
    the box) plus prefill (prompt length at the learned prefill speed; before
    the first real sample, assume prefill is 4x decode — conservative for CPU
    llama.cpp). 0 if speed is unknown (→ don't gate on time; the streaming
    deadline in complete() is the backstop)."""
    if _tok_s <= 0:
        return 0.0
    t = 1.3 * _TYPICAL_OUT.get(category, 200) / _tok_s
    if prompt_chars > 0:
        speed = _prefill_tok_s if _prefill_tok_s > 0 else 4.0 * _tok_s
        t += (prompt_chars / 4.0) / speed
    return t


def sort_key(category: str, prompt_chars: int) -> tuple:
    """Order for the local queue: short-output categories first (they save the
    most remote tokens per local second — sentiment/ner/factual before
    math/logic), grouped by category so llama.cpp's prefix cache reuses the
    shared system prompt, longest prompts last within a category."""
    return (_TYPICAL_OUT.get(category, 200), category, prompt_chars)


def available_for(category: str) -> bool:
    return _llm is not None and category in _CATEGORIES


def complete(system: str, prompt: str, max_tokens: int,
             deadline: float | None = None) -> LocalOut:
    """One local completion. gemma has no system role, so the instruction is
    folded into the user turn. Serialized: llama.cpp is not thread-safe.

    Streams token by token so generation is INTERRUPTIBLE: past `deadline`
    (absolute time.monotonic()) it stops and returns the partial text with
    truncated=True — the caller escalates it instead of trusting a cut-off
    answer. This is what lets local work run close to the harness kill (the
    v26 TIMEOUT failure mode); only the prefill of one prompt remains
    non-interruptible. Also updates the measured decode/prefill speeds from
    every call, so est_seconds() tracks the real box, not just the warmup."""
    global _tok_s, _prefill_tok_s
    t0 = time.monotonic()
    t_first = 0.0
    parts: list[str] = []
    n_out = 0
    cut = False
    with _lock:
        stream = _llm.create_chat_completion(
            messages=[{"role": "user", "content": f"{system}\n\n{prompt}"}],
            max_tokens=max_tokens,
            temperature=0,
            stream=True,
        )
        try:
            for chunk in stream:
                now = time.monotonic()
                if not t_first:
                    t_first = now
                choices = chunk.get("choices") or [{}]
                piece = (choices[0].get("delta") or {}).get("content")
                if piece:
                    parts.append(piece)
                    n_out += 1
                if deadline is not None and now > deadline:
                    cut = True
                    break        # hard stop: a truncated answer beats a TIMEOUT
        finally:
            stream.close()
    t_end = time.monotonic()
    if t_first:
        ttft = t_first - t0
        p_tok = (len(system) + len(prompt)) / 4.0
        if ttft > 0.05 and p_tok > 16:
            pf = p_tok / ttft
            _prefill_tok_s = pf if _prefill_tok_s <= 0 else 0.5 * (_prefill_tok_s + pf)
        if n_out >= 8 and t_end > t_first:
            dec = n_out / (t_end - t_first)
            _tok_s = dec if _tok_s <= 0 else 0.5 * (_tok_s + dec)
    return LocalOut("".join(parts).strip(), cut)


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
