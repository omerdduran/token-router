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

_llm = None
# llama.cpp is not thread-safe; the worker pool must serialize access to it.
_lock = threading.Lock()
_CATEGORIES = {c.strip() for c in os.environ.get("LOCAL_CATEGORIES", "").split(",") if c.strip()}


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


def available_for(category: str) -> bool:
    return _llm is not None and category in _CATEGORIES


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
