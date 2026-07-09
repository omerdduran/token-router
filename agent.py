"""Per-category answer strategy: classify, then one tuned Fireworks call.

Each category carries a terse system prompt, a token cap, and a model tier.
Prompts are deliberately short — input tokens count toward the score — and
push the model to answer directly without preamble.
"""

from __future__ import annotations

from classifier import Category, classify
from llm import complete, model_for

CHEAP, STRONG, CODE = "cheap", "strong", "code"

# Kept deliberately terse: the system prompt is input on every call and input
# tokens count toward the score. Trimming wording (not intent) lowers tokens
# with no effect on what the model outputs — an accuracy-neutral saving.
_BASE = "English only. Be concise; no preamble."

# (system prompt, max_tokens, tier) per category. Caps are ceilings, not
# targets: a concise answer stops well before them, so they are left generous
# to avoid ever truncating a correct answer into a judge failure.
_CONFIG: dict[Category, tuple[str, int, str]] = {
    Category.FACTUAL: (
        f"{_BASE} Explain clearly in under 120 words.",
        320, STRONG,
    ),
    Category.MATH: (
        f"{_BASE} Brief steps, then 'Answer: <value>' on its own line.",
        400, STRONG,
    ),
    Category.SENTIMENT: (
        f"{_BASE} Label the sentiment positive, negative, or neutral, "
        f"then one short reason.",
        120, CHEAP,
    ),
    Category.SUMMARIZATION: (
        f"{_BASE} Output only the summary; obey any stated length or "
        f"format constraint.",
        240, CHEAP,
    ),
    Category.NER: (
        f"{_BASE} List each entity as 'label: value', one per line; "
        f"labels: person, organization, location, date.",
        260, CHEAP,
    ),
    Category.CODE_DEBUG: (
        f"{_BASE} Name the bug in one sentence, then the corrected code "
        f"in one fenced block.",
        520, CODE,
    ),
    Category.CODE_GEN: (
        f"{_BASE} Output only the code in one fenced block, correct and "
        f"self-contained.",
        520, CODE,
    ),
    Category.LOGIC: (
        f"{_BASE} Deduce in brief numbered steps, checking each "
        f"constraint, then 'Answer: <value>' on its own line.",
        460, STRONG,
    ),
}


def solve(prompt: str) -> str:
    system, max_tokens, tier = _CONFIG[classify(prompt)]
    primary = model_for(tier)
    # Blank/failed answers retry on the opposite general tier.
    fallback = model_for(STRONG if tier == CHEAP else CHEAP)
    return complete(prompt, system=system, max_tokens=max_tokens,
                    model=primary, fallback_model=fallback)
