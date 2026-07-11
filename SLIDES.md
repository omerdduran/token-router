# TokenRouter

### A hybrid, token-efficient routing agent
**AMD Developer Hackathon · ACT II · Track 1**

*Answer as much as possible for free — pay the API only for what genuinely needs it.*

---

## The problem

Track 1 ranks passing submissions by **fewest total Fireworks tokens**.

- Every token that reaches the API is scored — prompt *and* hidden "thinking".
- The accuracy gate must still be cleared.

**The cheapest token is the one you never send.**

So the real game isn't "call the model efficiently" — it's **"don't call the
model at all when you don't have to."**

---

## Architecture — zero-token layers first

```
  task ─► 1. Classify     regex → (miss) local model      0 tokens
          2. Solvers      logic puzzles + arithmetic       0 tokens
          3. Local model  gemma-4-E2B, in the image        0 tokens
                          math · sentiment · NER ·
                          summarization · factual
          4. Fireworks    code · logic (+ any local miss)  paid, minimal
  ───────────────────────────────────────────────────────────────────► answer
```

The first three layers cost nothing. Only what survives to layer 4 spends
tokens. **Five of eight categories never reach the API.**

---

## The local Gemma engine

A `gemma-4-E2B-it` GGUF (Q3\_K\_M, ~2.5 GB) is **baked into the container** and
run on CPU with `llama.cpp`.

- Answers **math, sentiment, NER, summarization, factual** at **zero tokens**.
- Also powers the **semantic classifier fallback**: when regex matches nothing,
  Gemma decides the category instead of blindly defaulting — robust to reworded
  and messy prompts, still free.

One small local model does both the routing *and* most of the answering.

---

## Why E2B — we benchmarked 16 models

We tested **16 small models over two rounds** (gemma-2-2b, gemma-4-E2B, Qwen2.5
/ Qwen3 sizes, Qwen-Coder, Phi-3.5, Llama-3.2-3B, …) on all eight categories —
accuracy, output length, and speed on the 4 GB / 2 vCPU / CPU box.

| Model | Fits 4 GB | Fast on 2 vCPU | Accuracy |
|---|---|---|---|
| gemma-2-2b | ✅ | ✅ | weak (math 58%) |
| **gemma-4-E2B** | ✅ | ✅ | **strong (math 100%)** ← pick |
| Qwen3-4B | ✅ | ❌ slow → sheds to API | strong |
| Qwen3-1.7B / 0.6B | ✅ | ✅ | drops off |

**gemma-4-E2B is Pareto-optimal** — fast *and* accurate enough. It also keeps
the whole stack in the Gemma family.

---

## Adaptive & reliable — never times out

The judging box speed is unknown until runtime, so routing **adapts to it**:

- A startup warmup measures the box's **tokens/second**.
- A task is kept local only if its **estimated time fits the budget** —
  otherwise it escalates to Fireworks.
- A **global wall-clock ceiling** keeps the local loop + API pool from summing
  past the harness kill.

Fast box → everything local. Slow box → long work sheds to the API. Either way:
**no TIMEOUT**, and a valid `results.json` is always on disk (skeleton-first +
incremental flush + SIGTERM handler).

---

## Results

- **5 of 8 categories answered at zero Fireworks tokens** (math, sentiment, NER,
  summarization, factual) — plus deterministic solvers for structured
  logic/math.
- Only **code and logic** reach the paid API.
- **Validated on the organizers' public sample tasks**: explanatory factual
  (RGB vs RYB, RAM vs ROM), *mixed* sentiment (both sides named), strictly
  formatted summaries, 5-entity NER.
- Runs cleanly under `--memory 4g --cpus 2` — no OOM, no TIMEOUT.

---

## Gemma everywhere · AMD

- **Local Gemma-4-E2B** does the bulk of the work at zero cost, in-container.
- **Fireworks' Gemma tier** is available for escalation.
- CPU inference via `llama.cpp` on the AMD judging infra; the model search and
  fine-tuning experiments ran on **AMD ROCm + Unsloth**.

**TokenRouter: the most token-efficient answer is the one you compute for free —
and Gemma is what makes free possible.**
