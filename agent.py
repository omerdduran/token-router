"""Per-category answer strategy: classify, then one tuned Fireworks call.

Each category carries a terse system prompt, a token cap, and a model tier.
Prompts are deliberately short — input tokens count toward the score — and
push the model to answer directly without preamble.
"""

from __future__ import annotations

from classifier import Category, classify
from llm import complete, model_for
from solvers import solve_arithmetic, solve_logic

CHEAP, STRONG, CODE = "cheap", "strong", "code"

# Output-minimizing prompts: the answer only, no chain-of-thought, no
# explanation. Completion tokens are scored, and a gate-passing entry
# (TokenForge) proved bare answers hold accuracy at 84.2% — the judge scores
# the final answer, not the working. Caps fit a compliant short answer with
# headroom: a terse reply stops at EOS well before them, while a model that
# ignores "no steps" is truncated rather than billed in full.
_BASE = "English only. No preamble, no restating the task."

_CONFIG: dict[Category, tuple[str, int, str]] = {
    Category.FACTUAL: (
        f"{_BASE} Answer in one or two short sentences — the fact only.",
        200, STRONG,
    ),
    Category.MATH: (
        f"{_BASE} Give only 'Answer: <value>' (with units if relevant). "
        f"Show no steps or working.",
        120, STRONG,
    ),
    Category.SENTIMENT: (
        f"{_BASE} Reply with one label: positive, negative, or neutral. "
        f"Add a brief reason only if the task explicitly asks.",
        60, CHEAP,
    ),
    Category.SUMMARIZATION: (
        f"{_BASE} Output only the summary; obey any stated length or format "
        f"constraint. Nothing else.",
        220, CHEAP,
    ),
    Category.NER: (
        f"{_BASE} List each entity as 'label: value', one per line; labels: "
        f"person, organization, location, date. Nothing else.",
        200, CHEAP,
    ),
    Category.CODE_DEBUG: (
        f"{_BASE} Give only the corrected code in one fenced block. "
        f"No explanation.",
        512, CODE,
    ),
    Category.CODE_GEN: (
        f"{_BASE} Give only the code in one fenced block, correct and "
        f"self-contained. No explanation.",
        512, CODE,
    ),
    Category.LOGIC: (
        f"{_BASE} Give only 'Answer: <value>' — the final answer. Show no "
        f"reasoning.",
        120, STRONG,
    ),
}


# Zero-token deterministic solvers, tried per category before any model call.
# Each self-gates and returns None on the slightest doubt, so a miss simply
# falls through to the model — it can never turn a gettable task into a wrong
# answer. Order-independent: at most one fires per task.
_SOLVERS = {
    Category.LOGIC: solve_logic,
    Category.MATH: solve_arithmetic,
}


def solve(prompt: str) -> str:
    category = classify(prompt)
    solver = _SOLVERS.get(category)
    if solver is not None:
        try:
            answer = solver(prompt)
        except Exception:  # a solver bug must never break the task
            answer = None
        if answer:
            return answer

    system, max_tokens, tier = _CONFIG[category]
    primary = model_for(tier)
    # Blank/failed answers retry on the opposite general tier.
    fallback = model_for(STRONG if tier == CHEAP else CHEAP)
    return complete(prompt, system=system, max_tokens=max_tokens,
                    model=primary, fallback_model=fallback)
