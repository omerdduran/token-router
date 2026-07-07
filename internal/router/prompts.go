package router

import "tokenrouter/internal/classify"

// Every prompt token below is scored by the judging proxy — these are as
// short as the accuracy gate allows. Category-specific format scaffolding
// only where the judge needs a specific shape (NER lines, length limits).

var remoteSystem = map[classify.Category]string{
	classify.Factual:   "Answer concisely.",
	classify.Math:      "Solve. End with 'Answer: <number>'.",
	classify.Sentiment: "Sentiment (Positive/Negative/Neutral/Mixed) + one-sentence justification. Purely factual reporting is Neutral; sarcastic praise is Negative.",
	classify.Summarize: "Summarize as instructed, obey length limits exactly.",
	classify.NER:       "List entities as '<entity> — <person|organization|location|date>', one per line. Include relative dates.",
	classify.CodeDebug: "Name the bug in one sentence, then corrected code only.",
	classify.Logic:     "Reason step by step briefly. End with 'Answer: <solution>'.",
	classify.CodeGen:   "Output only the code.",
}

// genericSystem serves weak-signal tasks: the big remote models handle any
// category well from the task text itself.
const genericSystem = "Answer the task directly and concisely."

// palSystem: the model only translates the word problem; Go does the
// arithmetic. ~20 output tokens instead of a 150-token worked solution, and
// the arithmetic can't be wrong.
const palSystem = "Convert the problem to ONE arithmetic expression using only numbers and + - * / ^ ( ). No words, no equals sign. If not expressible as pure arithmetic, output UNSUPPORTED."

// Stop sequences trim trailing filler (a second paragraph, sign-offs) that
// would otherwise be billed as completion tokens. "\n\n" is the safe universal
// choice: it preserves single-line and single-paragraph answers and only cuts
// what follows a blank line. NEVER "\n" for NER (a multi-line entity list) or
// code (blank lines are structural) — it would truncate the real answer.
var remoteStop = map[classify.Category][]string{
	classify.Sentiment: {"\n\n"},
	classify.Factual:   {"\n\n"},
	classify.Summarize: {"\n\n"},
	classify.NER:       {"\n\n"},
	// Math, Logic, CodeGen, CodeDebug: no stop (reasoning/code contain newlines).
}

var remoteMaxTokens = map[classify.Category]int{
	classify.Factual:   130,
	classify.Math:      150,
	classify.Sentiment: 40,
	classify.Summarize: 110,
	classify.NER:       120,
	classify.CodeDebug: 450,
	classify.Logic:     280,
	classify.CodeGen:   450,
}

const genericMaxTokens = 200
