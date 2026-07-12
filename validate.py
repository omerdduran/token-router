"""Deterministic answer validation and self-consistency (zero tokens).

Epistemic escalation support for the local model, in three layers:
  Layer 1 — verdict(): per-category format/refusal checks. A non-empty reason
            means the answer is confidently unusable → escalate to Fireworks.
  Layer 2 — flag(): categories whose accepted answers still deserve a free
            second opinion when idle local time remains.
  Layer 3 — verify_system()/agree(): an alternate-phrasing second pass and a
            mechanical comparison; only confident disagreement escalates.

The economics are asymmetric: a false reject burns ~300-700 Fireworks tokens,
a false pass is just the status quo. So every rule fires only when the answer
is confidently unusable (the solvers' self-gating philosophy).
"""

from __future__ import annotations

import ast
import os
import re

from solvers import _format_number

ENABLED = os.environ.get("VALIDATE", "true").strip().lower() in ("1", "true", "yes")


def _norm(s: str) -> str:
    """Lowercase, strip punctuation, collapse whitespace — for substring and
    set comparisons that must survive case/markdown/possessive noise."""
    return " ".join(re.sub(r"[^a-z0-9 ]+", " ", (s or "").lower()).split())


# --- Hedging / refusal --------------------------------------------------------
# Scanned only in the answer's head: refusals lead; a whole-text scan would
# false-fire on phrases inside a long chain of thought.
_HEDGE_WINDOW = 160
_HEDGE_COMMON = re.compile(
    r"as an ai|i apologize|i(?:'m| am) (?:just|only) a\b|"
    r"i don'?t have (?:access|information|enough information)",
    re.IGNORECASE)
# logic is exempt: "cannot (be) determined" is a legitimate puzzle answer.
_HEDGE_STRICT = re.compile(
    r"i don'?t know|i do not know|i(?:'m| am) not sure|"
    r"i cannot|i can'?t|i(?:'m| am) unable",
    re.IGNORECASE)


# --- Shared answer parsing ----------------------------------------------------
# The math/logic system prompts mandate a final "Answer: <value>" line
# (agent.py); nothing else in the codebase extracts it. Bold-tolerant, and the
# LAST match wins (a CoT may restate "Answer:" mid-reasoning).
_ANSWER_LINE = re.compile(r"^\s*\**\s*answer\s*[:=]\s*(.+?)\s*\**\s*$",
                          re.IGNORECASE | re.MULTILINE)
_BULLET_PREFIX = re.compile(r"^(?:[-*•]\s*|\d+[.)]\s*)")


def _answer_line(text: str) -> str | None:
    matches = _ANSWER_LINE.findall(text or "")
    return matches[-1].strip() if matches else None


def _answer_number(text: str) -> str | None:
    """Canonical numeric value of an Answer: line, or None when it can't be
    pinned down to exactly one number (ambiguity → vacuous agreement)."""
    val = _answer_line(text)
    if val is None:
        return None
    cleaned = val.replace(",", "").replace("$", "").replace("%", "")
    nums = re.findall(r"-?\d+(?:\.\d+)?", cleaned)
    if len(nums) != 1:
        return None
    try:
        return _format_number(float(nums[0]))
    except (ValueError, OverflowError):
        return None


# --- Layer 1: per-category checks ---------------------------------------------


def _check_math(prompt: str, answer: str) -> str:
    val = _answer_line(answer)
    if val is None:
        return "no-answer-line"
    if not re.search(r"\d", val):
        return "no-numeric-value"
    return ""


def _check_logic(prompt: str, answer: str) -> str:
    # A bare short conclusion ("Alice owns the bird.") is fine — the solvers
    # emit that shape too. Only long reasoning that never concludes is rejected.
    if _answer_line(answer) is not None or len(answer.strip()) <= 300:
        return ""
    return "no-conclusion"


_NUM_WORDS = {"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
              "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10}
# (?<![\d\-–]) rejects ranges like "2-3 sentences" — an ambiguous constraint
# is a self-gate, not a guess.
_SENT_REQ = re.compile(r"(?<![\d\-–])\b(one|two|three|four|five|\d+)\s+sentences?\b",
                       re.IGNORECASE)
_BULLET_REQ = re.compile(r"(?<![\d\-–])\b(one|two|three|four|five|\d+)\s+bullet(?:\s*points?|s)?\b",
                         re.IGNORECASE)
_WORD_CAP = re.compile(r"(?:under|at most|no more than|fewer than|less than)\s+(\d+)\s+words",
                       re.IGNORECASE)
# Abbreviation/initial/decimal dots are masked before sentence splitting so
# "Dr. Smith", "J. Doe" and "3.14" don't inflate the count. Under-splitting is
# the safe direction: we only reject when the count EXCEEDS the constraint.
_ABBR_DOT = re.compile(
    r"\b(?:Dr|Mr|Mrs|Ms|Prof|St|Jr|Sr|Inc|Ltd|Co|vs|etc|e\.g|i\.e|U\.S|U\.K|No|Fig|al)\.",
    re.IGNORECASE)


def _req_count(pattern: re.Pattern, prompt: str) -> int | None:
    """The constraint's number, only when the prompt states it exactly once."""
    found = pattern.findall(prompt)
    if len(found) != 1:
        return None
    tok = found[0].lower()
    return _NUM_WORDS.get(tok) or int(tok)


def _count_sentences(text: str) -> int:
    t = (text or "").strip()
    if not t:
        return 0
    t = _ABBR_DOT.sub(lambda m: m.group(0).replace(".", "\x00"), t)
    t = re.sub(r"\b([A-Z])\.(?=\s+[A-Z])", "\\1\x00", t)   # initials: "J. Smith"
    t = re.sub(r"(\d)\.(\d)", "\\1\x00\\2", t)             # decimals: "3.14"
    return len([p for p in re.split(r"[.!?]+(?:\s+|$)", t) if p.strip()])


def _bullet_items(text: str) -> list[str]:
    return [_BULLET_PREFIX.sub("", ln.strip())
            for ln in (text or "").splitlines()
            if _BULLET_PREFIX.match(ln.strip()) and _BULLET_PREFIX.sub("", ln.strip())]


def _check_summarization(prompt: str, answer: str) -> str:
    n_bullets = _req_count(_BULLET_REQ, prompt)
    if n_bullets is not None:
        items = _bullet_items(answer)
        if len(items) != n_bullets:   # bullet counting is mechanically reliable
            return "bullet-count"
    n_sentences = _req_count(_SENT_REQ, prompt)
    if n_sentences is not None and n_bullets is None:
        if _count_sentences(answer) > n_sentences:   # over-count only
            return "sentence-count"
    caps = _WORD_CAP.findall(prompt)
    if len(caps) == 1:
        cap = int(caps[0])
        m = _WORD_CAP.search(prompt)
        per_item = "each" in prompt[max(0, m.start() - 30):m.start()].lower()
        if per_item and n_bullets is not None:
            items = _bullet_items(answer)
        elif per_item and n_sentences is not None:
            items = [answer]   # per-sentence caps: too fuzzy to enforce → whole
        elif per_item:
            items = []         # "each" without a countable unit → self-gate
        else:
            items = [answer]   # "in under 50 words" → whole answer
        for item in items:
            if len(item.split()) > cap:   # exactly N passes (boundary leniency)
                return "word-limit"
    return ""


_NER_LINE = re.compile(r"^(person|organization|location|date)s?\s*[:\-]\s*(\S.*)$",
                       re.IGNORECASE)


def _ner_lines(text: str):
    """Parsed (label, value) entity lines, or None on a confidently-bad format.
    Short header lines ('Entities:') are tolerated; prose is not."""
    ents: list[tuple[str, str]] = []
    for raw in (text or "").splitlines():
        line = _BULLET_PREFIX.sub("", raw.strip()).replace("**", "").strip()
        if not line:
            continue
        m = _NER_LINE.match(line)
        if m:
            ents.append((m.group(1).lower().rstrip("s"), m.group(2).strip()))
        elif len(line) <= 60 and line.endswith(":"):
            continue   # header line, harmless
        else:
            return None
    return ents


def _check_ner(prompt: str, answer: str) -> str:
    ents = _ner_lines(answer)
    if ents is None:
        return "bad-ner-format"
    if not ents:
        return "no-entities"
    src = _norm(prompt)
    for label, value in ents:
        if label == "date":   # models re-format dates; exempt from grounding
            continue
        for part in value.split(","):
            p = _norm(part)
            if len(p) >= 3 and p not in src:
                return "ner-hallucination"
    return ""


# Tolerates a missing closing fence — the local model sometimes stops at the
# token cap right after the code, which is still perfectly usable code.
_FENCE = re.compile(r"```(\w*)[ \t]*\n(.*?)(?:```|$)", re.DOTALL)
_PYTHONIC = re.compile(r"\b(?:def |import |class )")


def _check_code(prompt: str, answer: str) -> str:
    blocks = _FENCE.findall(answer or "")
    if not blocks:
        return "no-code-block"
    checked = ok = False
    for tag, body in blocks:
        tag = tag.lower()
        if tag in ("python", "py") or (not tag and _PYTHONIC.search(body)):
            checked = True
            try:
                ast.parse(body)
                ok = True
            except SyntaxError:
                pass
    if checked and not ok:
        return "syntax-error"
    return ""


_SENT_LABEL = re.compile(r"\b(positive|negative|neutral|mixed)\b", re.IGNORECASE)


def _check_sentiment(prompt: str, answer: str) -> str:
    return "" if _SENT_LABEL.search(answer) else "no-label"


_CHECKS = {
    "math": _check_math,
    "logic": _check_logic,
    "summarization": _check_summarization,
    "ner": _check_ner,
    "code_gen": _check_code,
    "code_debug": _check_code,
    "sentiment": _check_sentiment,
    # factual: hedging only — there is no format to check.
}


def verdict(category: str, prompt: str, answer: str) -> str:
    """'' when the answer is acceptable, else a short reason slug."""
    head = (answer or "")[:_HEDGE_WINDOW]
    if _HEDGE_COMMON.search(head):
        return "hedge"
    if category != "logic" and _HEDGE_STRICT.search(head):
        return "hedge"
    check = _CHECKS.get(category)
    return check(prompt or "", answer or "") if check else ""


# --- Layer 2: flag policy -------------------------------------------------------
# factual is the local model's epistemically weakest category — always worth a
# second opinion. math/logic/sentiment/ner have mechanically comparable answers,
# so idle verification is free. summarization/code have no reliable comparison
# (two different summaries/programs can both be right); Layer 1 is their guard.
_FLAGGED = {"factual", "math", "logic", "sentiment", "ner"}

_DOUBT = re.compile(
    r"\b(?:probably|i think|i believe|might be|may be|roughly|approximately|"
    r"not (?:entirely |completely )?certain)\b", re.IGNORECASE)


def flag(category: str) -> bool:
    return category in _FLAGGED


def soft_doubt(answer: str) -> bool:
    """Prioritizes the verify queue only — never escalates by itself."""
    return bool(_DOUBT.search((answer or "")[:400]))


# --- Layer 3: second pass + agreement -------------------------------------------
# The second pass reuses the same task at temperature 0 with a DIFFERENTLY
# worded instruction (temp>0 would add sampling noise and make mismatches
# non-reproducible). math/logic keep their CoT: a no-CoT re-ask would be wrong
# more often and manufacture disagreements on tasks the first pass got right.
_VERIFY_SYSTEM = {
    "math": ("Solve the problem carefully. Think step by step, then finish "
             "with 'Answer: <value>' on its own line."),
    "logic": ("Work through the puzzle, checking each clue one by one, then "
              "finish with 'Answer: <value>' on its own line."),
    "sentiment": "Reply with exactly one word: positive, negative, neutral, or mixed.",
    "ner": ("Output only lines of the form 'label: value', using the labels "
            "person, organization, location, date."),
    "factual": "Answer in one short phrase only - no explanation.",
}
_VERIFY_CAP = {"sentiment": 8, "factual": 32}


def verify_system(category: str) -> str:
    return _VERIFY_SYSTEM.get(category, "")


def verify_max_tokens(category: str, default: int) -> int:
    return _VERIFY_CAP.get(category, default)


_ARTICLES = re.compile(r"\b(?:the|a|an)\b")
_LABEL_CLASS = {"positive": "pos", "negative": "neg",
                # the agent prompt treats neutral/mixed as interchangeable
                "neutral": "mix", "mixed": "mix"}
_STOPWORDS = {"the", "and", "are", "was", "were", "its", "has", "had", "but",
              "not", "this", "that", "with", "from", "for", "which", "have"}


def _agree_math(first: str, second: str) -> bool:
    a, b = _answer_number(first), _answer_number(second)
    if a is None or b is None:
        return True   # vacuous: never escalate on our own parsing doubt
    return a == b


def _norm_conclusion(text: str) -> str:
    val = _answer_line(text)
    if val is None:
        stripped = (text or "").strip()
        val = stripped if len(stripped) <= 300 else ""
    return " ".join(_ARTICLES.sub(" ", _norm(val)).split())


def _agree_logic(first: str, second: str) -> bool:
    a, b = _norm_conclusion(first), _norm_conclusion(second)
    if not a or not b:
        return True
    return a == b or a in b or b in a


def _agree_sentiment(first: str, second: str) -> bool:
    ma, mb = _SENT_LABEL.search(first or ""), _SENT_LABEL.search(second or "")
    if not ma or not mb:
        return True
    return _LABEL_CLASS[ma.group(1).lower()] == _LABEL_CLASS[mb.group(1).lower()]


def _entity_set(text: str) -> set[tuple[str, str]]:
    ents = _ner_lines(text)
    out: set[tuple[str, str]] = set()
    for label, value in ents or []:
        for part in value.split(","):
            p = _norm(part)
            if p:
                out.add((label, p))
    return out


def _agree_ner(first: str, second: str) -> bool:
    a, b = _entity_set(first), _entity_set(second)
    if not a or not b:
        return True
    return len(a & b) / len(a | b) >= 0.5


def _agree_factual(first: str, second: str) -> bool:
    # The short second answer's content words should appear in the long first
    # answer ("Canberra" anywhere in the 120-word answer is agreement).
    toks = [w for w in re.findall(r"[a-z0-9]+", (second or "").lower())
            if len(w) >= 3 and w not in _STOPWORDS]
    if not toks:
        return True
    hay = _norm(first)
    hits = sum(1 for w in toks if w in hay)
    return hits >= max(1, (len(toks) + 1) // 2)


_AGREE = {
    "math": _agree_math,
    "logic": _agree_logic,
    "sentiment": _agree_sentiment,
    "ner": _agree_ner,
    "factual": _agree_factual,
}


def agree(category: str, first: str, second: str) -> bool:
    fn = _AGREE.get(category)
    return fn(first or "", second or "") if fn else True
