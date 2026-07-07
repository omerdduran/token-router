package router

import "tokenrouter/internal/classify"

// Local prompts are free (zero scored tokens), so they can afford detail and
// format scaffolding. Remote prompts cost leaderboard tokens: keep them as
// short as the accuracy gate allows.

const localBase = "You are a precise assistant. Answer in English. No preamble, no self-reference. "

var localSystem = map[classify.Category]string{
	classify.Factual:   localBase + "Answer factually and concisely in 2-4 sentences.",
	classify.Math:      localBase + "Solve step by step, then give the final line as 'Answer: <number>'.",
	classify.Sentiment: localBase + "Classify the sentiment as Positive, Negative, Neutral, or Mixed, then justify it in one short sentence. Rules: text that merely reports facts is Neutral even if the facts are emotionally charged; sarcastic praise is Negative; if both sides appear but the author lands on a clear overall verdict, use that side rather than Mixed.",
	classify.Summarize: localBase + "Summarize exactly as instructed. Obey any length or format constraint strictly.",
	classify.NER:       localBase + "Extract the requested entities. Output one entity per line as '<entity> — <type>'. Types: person, organization, location, date. Include every date, even relative ones like 'last October' or 'Wednesday'. No other text.",
	classify.CodeDebug: localBase + "Identify the bug briefly, then output the corrected code in a single fenced code block.",
	classify.Logic:     localBase + "Reason carefully step by step, then give the final line as 'Answer: <solution>'.",
	classify.CodeGen:   localBase + "Write the requested code. Output only a single fenced code block, then one sentence of usage notes if needed.",
}

// MathToExpr asks the local model only to translate a word problem to an
// arithmetic expression; Go evaluates it exactly (zero tokens, zero
// arithmetic slips).
const mathToExprSystem = "Convert the problem to ONE arithmetic expression using only numbers and + - * / ^ ( ). No words, no equals sign, no explanation. If it cannot be expressed as pure arithmetic, output UNSUPPORTED."

var remoteSystem = map[classify.Category]string{
	classify.Factual:   "Answer concisely.",
	classify.Math:      "Solve. End with 'Answer: <number>'.",
	classify.Sentiment: "Sentiment (Positive/Negative/Neutral/Mixed) + one-sentence justification.",
	classify.Summarize: "Summarize as instructed, obey length limits.",
	classify.NER:       "List entities as '<entity> — <person|organization|location|date>', one per line.",
	classify.CodeDebug: "Fix the bug. Output corrected code only.",
	classify.Logic:     "Solve. End with 'Answer: <solution>'.",
	classify.CodeGen:   "Output only the code.",
}

// Token caps. Local caps are about latency (CPU decode time), remote caps
// are scored tokens — keep them tight per category.
var localMaxTokens = map[classify.Category]int{
	classify.Factual:   220,
	classify.Math:      550, // hard multi-step problems truncated at 280 before reaching 'Answer:'
	classify.Sentiment: 60,
	classify.Summarize: 160,
	classify.NER:       160,
	classify.CodeDebug: 500,
	classify.Logic:     900, // zebra-scale derivations truncated at 600 mid-conclusion
	classify.CodeGen:   500,
}

var remoteMaxTokens = map[classify.Category]int{
	classify.Factual:   130,
	classify.Math:      120,
	classify.Sentiment: 40,
	classify.Summarize: 110,
	classify.NER:       120,
	classify.CodeDebug: 450,
	classify.Logic:     280,
	classify.CodeGen:   450,
}
