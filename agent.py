"""Per-category answer strategy: classify, then one tuned Fireworks call.

Each category carries a terse system prompt, a token cap, and a model tier.
Prompts are deliberately short — input tokens count toward the score — and
push the model to answer directly without preamble.
"""

from __future__ import annotations

import os
import re

import local
from classifier import Category, classify
from llm import complete, model_for
from solvers import solve_logic, solve_math

CHEAP, STRONG, CODE = "cheap", "strong", "code"

# Proven config (v3, 16/19 on the leaderboard): the strong tier is a
# reasoning model (minimax-m3) run with reasoning_effort=none, so it can only
# reason in VISIBLE chain-of-thought. Removing the "brief steps" instruction
# (v7) collapsed accuracy to 12/19 — the CoT that costs completion tokens is
# also what carries correctness on math/logic here. Do not trim it.
_BASE = "Answer in English. Be concise and direct; no preamble, no restating the question."

_CONFIG: dict[Category, tuple[str, int, str]] = {
    # Factual is recall: minimax under reasoning_effort=none answers it cheaply
    # (short, no hidden thinking). Routing it to Gemma cost MORE (measured, v16:
    # gemma needs effort=low → thinking tokens), and the local 2-3B is only ~70%
    # here, too risky for the gate. So keep factual on the strong tier.
    Category.FACTUAL: (
        f"{_BASE} Give a correct, clear answer in under 120 words.",
        320, STRONG,
    ),
    Category.MATH: (
        f"{_BASE} Work through it in brief steps, then end with "
        f"'Answer: <value>' on its own line.",
        300, STRONG,
    ),
    Category.SENTIMENT: (
        f"{_BASE} State the sentiment as positive, negative, neutral, or mixed. "
        f"Then give a one-sentence reason. If the text has both good and bad "
        f"points, say 'mixed' (or 'neutral') and your reason MUST mention both "
        f"the positive and the negative aspects.",
        120, CHEAP,
    ),
    Category.SUMMARIZATION: (
        f"{_BASE} Output only the summary and obey any length or format "
        f"constraint stated in the task.",
        240, CHEAP,
    ),
    Category.NER: (
        f"{_BASE} List each entity as 'label: value', one per line, using "
        f"the labels person, organization, location, date.",
        260, CHEAP,
    ),
    Category.CODE_DEBUG: (
        f"{_BASE} State the bug in one sentence, then give the corrected "
        f"code in a single fenced block.",
        400, CODE,
    ),
    Category.CODE_GEN: (
        f"{_BASE} Output only the code in a single fenced block — correct, "
        f"complete, and self-contained.",
        400, CODE,
    ),
    Category.LOGIC: (
        f"{_BASE} Reason in brief numbered steps, checking each constraint, "
        f"then end with 'Answer: <value>' on its own line.",
        350, STRONG,
    ),
}


# Zero-token deterministic solvers, tried per category before any model call.
# Each self-gates and returns None on the slightest doubt, so a miss simply
# falls through to the model — it can never turn a gettable task into a wrong
# answer. Order-independent: at most one fires per task.
_SOLVERS = {
    Category.LOGIC: solve_logic,
    Category.MATH: solve_math,
}


def route(prompt: str, allow_local: bool = True):
    """Resolve a task as far as possible without a paid call.

    Returns ("done", answer) when a deterministic solver or the local model
    already answered it, or ("remote", category, system, max_tokens, tier)
    when it still needs Fireworks. allow_local=False skips the (potentially
    slow) local model so a nearly-exhausted time budget still finishes on
    Fireworks instead of risking a timeout/INFRA_ERROR."""
    category = classify(prompt)
    solver = _SOLVERS.get(category)
    if solver is not None:
        try:
            answer = solver(prompt)
        except Exception:  # a solver bug must never break the task
            answer = None
        if answer:
            return ("done", answer)

    system, max_tokens, tier = _CONFIG[category]
    # try_reserve gates on both availability AND the remaining local-time budget,
    # so a slow box sheds long-output tasks to Fireworks instead of timing out.
    if allow_local and local.try_reserve(category.value):
        try:
            answer = local.complete(system, prompt, max_tokens)
        except Exception:
            answer = ""
        if answer:
            return ("done", answer)

    return ("remote", category, system, max_tokens, tier)


def solve_remote(prompt: str, system: str, max_tokens: int, tier: str) -> str:
    primary = model_for(tier)
    fallback = model_for(STRONG if tier == CHEAP else CHEAP)
    return complete(prompt, system=system, max_tokens=max_tokens,
                    model=primary, fallback_model=fallback)


def solve(prompt: str) -> str:
    r = route(prompt)
    if r[0] == "done":
        return r[1]
    _, category, system, max_tokens, tier = r
    return solve_remote(prompt, system, max_tokens, tier)


# --- Batching (opt-in via BATCH=true) ---------------------------------------
# Same-category Fireworks tasks are answered in one call, so the system prompt
# and per-message scaffolding are paid once instead of once per task. Only
# short, single-answer categories are batched; a numbered-reply parse failure
# falls the whole group back to individual calls (never a wrong answer).
_BATCH = os.environ.get("BATCH", "").strip().lower() in ("1", "true", "yes")
_BATCH_CATEGORIES = {c.strip() for c in
                     os.environ.get("BATCH_CATEGORIES", "sentiment,factual").split(",")
                     if c.strip()}
# A marker on its own line separates answers. Unlike a bare "k.", it can't
# collide with a numbered chain-of-thought step inside an answer, so math and
# logic (which reason in numbered steps) can be batched safely too.
_MARK_SPLIT = re.compile(r"(?m)^\s*===ANSWER\s+(\d+)===\s*$")


def batchable(category: Category) -> bool:
    return _BATCH and category.value in _BATCH_CATEGORIES


def answer_batch(system: str, max_tokens: int, tier: str, prompts: list[str]):
    """One batched call for same-category prompts. Returns a list of answers
    aligned to prompts, or None if the reply can't be parsed cleanly."""
    n = len(prompts)
    batch_system = (
        f"{system} You are given {n} independent tasks, numbered 1 to {n}. "
        f"For each task k, first output a line containing exactly '===ANSWER k==='"
        f" on its own line, then task k's full answer on the lines below it. Do "
        f"this for all {n} tasks in order. Output nothing before '===ANSWER 1==='.")
    numbered = "\n\n".join(f"[Task {i + 1}]\n{p}" for i, p in enumerate(prompts))
    primary = model_for(tier)
    try:
        resp = complete(numbered, system=batch_system,
                        max_tokens=min(max_tokens * n + 80, 2200),
                        model=primary, fallback_model=None)
    except Exception:
        return None
    parts = _MARK_SPLIT.split(resp.strip())
    answers: dict[int, str] = {}
    for i in range(1, len(parts) - 1, 2):
        try:
            k = int(parts[i])
        except ValueError:
            continue
        answers[k] = parts[i + 1].strip()
    if all(j in answers and answers[j] for j in range(1, n + 1)):
        return [answers[j] for j in range(1, n + 1)]
    return None
