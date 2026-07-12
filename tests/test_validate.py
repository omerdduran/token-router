"""validate.py: pure-string tests — no model, no network.

The economics under test are asymmetric: a false reject burns tokens, a false
pass is the status quo. The '# --- Must pass ---' cases below are the ones
that would burn tokens if a validator over-fired.
"""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

import validate
from validate import agree, flag, soft_doubt, verdict, verify_max_tokens, verify_system


class TestMathVerdict(unittest.TestCase):
    # --- Must pass ---
    def test_pass_answer_line(self):
        self.assertEqual(verdict("math", "", "1. 240*0.15=36\nAnswer: 36"), "")

    def test_pass_bold_answer_line(self):
        self.assertEqual(verdict("math", "", "steps...\n**Answer: 42**"), "")

    def test_pass_dollar_commas_percent(self):
        self.assertEqual(verdict("math", "", "Answer: $1,234.50"), "")

    def test_pass_value_with_digit_and_symbols(self):
        self.assertEqual(verdict("math", "", "Answer: x = 5"), "")

    # --- Must reject ---
    def test_reject_missing_answer_line(self):
        self.assertEqual(verdict("math", "", "The result is probably 36."),
                         "no-answer-line")

    def test_reject_non_numeric_value(self):
        self.assertEqual(verdict("math", "", "Answer: it depends"),
                         "no-numeric-value")


class TestLogicVerdict(unittest.TestCase):
    # --- Must pass ---
    def test_pass_answer_line(self):
        self.assertEqual(verdict("logic", "", "1. clue A\n2. clue B\nAnswer: Alice"), "")

    def test_pass_bare_short_sentence(self):
        self.assertEqual(verdict("logic", "", "Alice owns the bird."), "")

    def test_pass_cannot_be_determined(self):
        # legitimate puzzle conclusion — must NOT count as hedging for logic
        self.assertEqual(verdict("logic", "", "It cannot be determined."), "")

    # --- Must reject ---
    def test_reject_long_ramble_without_answer_line(self):
        ramble = "Let us consider the clues carefully. " * 20
        self.assertEqual(verdict("logic", "", ramble), "no-conclusion")


class TestSummarizationVerdict(unittest.TestCase):
    # --- Must pass (self-gating) ---
    def test_pass_when_no_constraint_in_prompt(self):
        self.assertEqual(
            verdict("summarization", "Summarize the following text: ...",
                    "One. Two. Three. Four. Five sentences here."), "")

    def test_pass_exact_sentence_count(self):
        self.assertEqual(
            verdict("summarization", "Summarize in exactly 2 sentences: ...",
                    "The vote was postponed. Departments will revise plans."), "")

    def test_pass_range_constraint_is_ambiguous(self):
        # "2-3 sentences" is a range → self-gate, enforce nothing
        self.assertEqual(
            verdict("summarization", "Summarize in 2-3 sentences: ...",
                    "One. Two. Three. Four."), "")

    def test_abbreviation_dr_not_split(self):
        self.assertEqual(
            verdict("summarization", "Summarize in exactly 2 sentences: ...",
                    "Dr. Smith led the study. The results by Dr. Jones were clear."),
            "")

    def test_initials_and_decimals_not_split(self):
        self.assertEqual(
            verdict("summarization", "Summarize in two sentences: ...",
                    "J. Doe raised revenue by 3.14 percent. The board approved."), "")

    def test_pass_bullet_count(self):
        self.assertEqual(
            verdict("summarization", "Summarize as 3 bullet points: ...",
                    "- first point\n- second point\n- third point"), "")

    def test_pass_at_word_limit_boundary(self):
        self.assertEqual(
            verdict("summarization",
                    "Give 2 bullet points, each under 5 words: ...",
                    "- one two three four five\n- short line"), "")

    # --- Must reject ---
    def test_reject_too_many_sentences(self):
        self.assertEqual(
            verdict("summarization", "Summarize in exactly 2 sentences: ...",
                    "First. Second. Third. Fourth."), "sentence-count")

    def test_reject_bullet_count_mismatch(self):
        self.assertEqual(
            verdict("summarization", "Summarize as 3 bullet points: ...",
                    "- only one\n- and two"), "bullet-count")

    def test_reject_no_bullets_at_all(self):
        self.assertEqual(
            verdict("summarization", "Summarize as 3 bullet points: ...",
                    "A plain prose summary without any bullets."), "bullet-count")

    def test_reject_word_limit(self):
        self.assertEqual(
            verdict("summarization",
                    "Give 2 bullet points, each under 5 words: ...",
                    "- one two three four five six seven\n- fine"), "word-limit")


NER_SRC = "Satya Nadella met engineers at Microsoft's office in Seattle on March 3, 2024."


class TestNerVerdict(unittest.TestCase):
    # --- Must pass ---
    def test_pass_plain_label_lines(self):
        self.assertEqual(
            verdict("ner", NER_SRC,
                    "person: Satya Nadella\norganization: Microsoft\n"
                    "location: Seattle\ndate: March 3, 2024"), "")

    def test_pass_markdown_bullets_and_bold(self):
        self.assertEqual(
            verdict("ner", NER_SRC,
                    "- **person**: Satya Nadella\n- **location**: Seattle"), "")

    def test_pass_header_line_tolerated(self):
        self.assertEqual(
            verdict("ner", NER_SRC, "Entities:\nperson: Satya Nadella"), "")

    def test_pass_rewritten_date_lenient(self):
        self.assertEqual(verdict("ner", NER_SRC, "date: 2024-03-03"), "")

    def test_pass_possessive_and_case_normalized(self):
        # "Microsoft's" in source; answer says "microsoft" — normalization covers it
        self.assertEqual(verdict("ner", NER_SRC, "organization: microsoft"), "")

    # --- Must reject ---
    def test_reject_prose_answer(self):
        self.assertEqual(
            verdict("ner", NER_SRC,
                    "The text mentions Satya Nadella who works at Microsoft in Seattle."),
            "bad-ner-format")

    def test_reject_no_entities(self):
        self.assertEqual(verdict("ner", NER_SRC, "Entities:"), "no-entities")

    def test_reject_hallucinated_person(self):
        self.assertEqual(verdict("ner", NER_SRC, "person: Bill Gates"),
                         "ner-hallucination")


class TestCodeVerdict(unittest.TestCase):
    # --- Must pass ---
    def test_pass_fenced_python(self):
        self.assertEqual(
            verdict("code_gen", "", "```python\ndef f(n):\n    return n + 1\n```"), "")

    def test_pass_missing_closing_fence(self):
        self.assertEqual(
            verdict("code_gen", "", "```python\ndef f(n):\n    return n + 1"), "")

    def test_pass_non_python_fence_unparsed(self):
        self.assertEqual(
            verdict("code_gen", "", "```javascript\nconst x = => broken\n```"), "")

    def test_pass_debug_prose_plus_block(self):
        self.assertEqual(
            verdict("code_debug", "",
                    "The bug is off-by-one.\n```python\ndef f(n):\n    return n\n```"),
            "")

    # --- Must reject ---
    def test_reject_no_fence(self):
        self.assertEqual(verdict("code_gen", "", "def f(n): return n + 1"),
                         "no-code-block")

    def test_reject_python_syntax_error(self):
        self.assertEqual(
            verdict("code_gen", "", "```python\ndef f(n:\n    return\n```"),
            "syntax-error")


class TestSentimentVerdict(unittest.TestCase):
    def test_pass_label_present(self):
        self.assertEqual(
            verdict("sentiment", "", "Mixed. Battery is great but screen scratches."), "")

    def test_reject_no_label(self):
        self.assertEqual(verdict("sentiment", "", "The review praises the battery."),
                         "no-label")


class TestHedging(unittest.TestCase):
    CASES = [
        ("factual", "I don't know the answer to this question."),
        ("factual", "As an AI, I cannot browse the internet."),
        ("math", "I'm not sure how to solve this.\nAnswer: 3"),
        ("summarization", "I apologize, but the text is unclear."),
    ]

    def test_reject_hedges(self):
        for cat, ans in self.CASES:
            with self.subTest(category=cat, answer=ans):
                self.assertEqual(verdict(cat, "", ans), "hedge")

    def test_logic_exempt_from_strict_hedges(self):
        self.assertEqual(verdict("logic", "", "I cannot determine the owner.\nAnswer: unknown"), "")

    def test_hedge_after_prefix_window_passes(self):
        long_head = "The capital of Australia is Canberra. " * 6   # > 160 chars
        self.assertEqual(verdict("factual", "", long_head + "I'm not sure though."), "")


class TestFlag(unittest.TestCase):
    CASES = [("factual", True), ("math", True), ("logic", True),
             ("sentiment", True), ("ner", True),
             ("summarization", False), ("code_gen", False), ("code_debug", False)]

    def test_flag_categories(self):
        for cat, expected in self.CASES:
            with self.subTest(category=cat):
                self.assertEqual(flag(cat), expected)

    def test_soft_doubt_phrases(self):
        self.assertTrue(soft_doubt("It is probably Canberra."))
        self.assertTrue(soft_doubt("I think the answer is 5."))
        self.assertFalse(soft_doubt("The capital is Canberra."))

    def test_verify_prompts_exist_for_flagged(self):
        for cat, expected in self.CASES:
            if expected:
                with self.subTest(category=cat):
                    self.assertTrue(verify_system(cat))

    def test_verify_caps(self):
        self.assertEqual(verify_max_tokens("sentiment", 120), 8)
        self.assertEqual(verify_max_tokens("factual", 320), 32)
        self.assertEqual(verify_max_tokens("math", 300), 300)


class TestAgree(unittest.TestCase):
    # --- math ---
    def test_math_numeric_equal_across_formats(self):
        for second in ("Answer: 36", "Answer: 36.0", "Answer: $36"):
            with self.subTest(second=second):
                self.assertTrue(agree("math", "steps\nAnswer: 36", second))

    def test_math_mismatch(self):
        self.assertFalse(agree("math", "Answer: 36", "Answer: 42"))

    def test_math_unparseable_second_is_vacuous_agree(self):
        self.assertTrue(agree("math", "Answer: 36", "I could not solve it."))
        self.assertTrue(agree("math", "Answer: 36", "Answer: 3 or 4"))  # ambiguous

    # --- logic ---
    def test_logic_containment(self):
        self.assertTrue(agree("logic", "Answer: Alice", "Alice owns the bird."))

    def test_logic_mismatch(self):
        self.assertFalse(agree("logic", "Answer: Alice owns the bird",
                               "Answer: Bob owns the bird"))

    # --- sentiment ---
    def test_sentiment_neutral_mixed_equivalent(self):
        self.assertTrue(agree("sentiment", "Mixed. Good battery, bad screen.",
                              "neutral"))

    def test_sentiment_mismatch(self):
        self.assertFalse(agree("sentiment", "Positive overall.", "negative"))

    def test_sentiment_missing_label_vacuous(self):
        self.assertTrue(agree("sentiment", "Positive overall.", "hard to say"))

    # --- ner ---
    def test_ner_jaccard_above_threshold(self):
        first = "person: Satya Nadella\norganization: Microsoft\nlocation: Seattle"
        second = "person: Satya Nadella\norganization: Microsoft"
        self.assertTrue(agree("ner", first, second))

    def test_ner_jaccard_below_threshold(self):
        self.assertFalse(agree("ner", "person: Satya Nadella\nlocation: Seattle",
                               "person: Bill Gates\nlocation: Redmond"))

    def test_ner_empty_second_vacuous(self):
        self.assertTrue(agree("ner", "person: Satya Nadella", "no entities found"))

    # --- factual ---
    def test_factual_token_overlap_agree(self):
        first = ("The capital of Australia is Canberra. It is located near the "
                 "Australian Capital Territory and was purpose-built.")
        self.assertTrue(agree("factual", first, "Canberra"))

    def test_factual_token_overlap_disagree(self):
        first = "The capital of Australia is Canberra."
        self.assertFalse(agree("factual", first, "Sydney"))

    def test_factual_empty_second_vacuous(self):
        self.assertTrue(agree("factual", "The capital is Canberra.", ""))

    # --- unknown category ---
    def test_unflagged_category_vacuous(self):
        self.assertTrue(agree("summarization", "a summary", "another summary"))


if __name__ == "__main__":
    unittest.main()
