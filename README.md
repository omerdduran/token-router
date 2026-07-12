# TokenRouter

A **token-efficient hybrid routing agent** for the AMD Developer Hackathon
(ACT II) — Track 1. The contest ranks passing submissions by **fewest total
Fireworks tokens**, subject to an LLM-judge accuracy gate. TokenRouter's whole
strategy is therefore simple: **answer as much as possible for free, and only
pay the API for what genuinely needs it.**

Four of the eight task categories are answered entirely inside the container at
**zero Fireworks tokens** by a bundled Gemma model; only the hardest remainder
escalates to the paid API — with terse prompts, tight token caps, and
same-category batching. Latest scored run: **100% accuracy**.

## How it works

Each task falls through four layers. The first three cost **nothing** against
the scored token budget; only the last one spends tokens.

```
                       ┌──────────────────────────────────────────┐
  /input/tasks.json ─► │  1. Classify        regex, zero-cost      │  0 tokens
                       │  2. Solvers         logic + arithmetic     │  0 tokens
                       │  3. Local model     gemma-4-E2B (in image) │  0 tokens
                       │        math · sentiment · NER ·            │
                       │        summarization                      │
                       │  4. Fireworks       factual · code · logic │  paid, minimal
                       │        (+ any local blank/overrun)         │
                       └──────────────────────────────────────────┘ ─► /output/results.json
```

1. **Zero-token classification** (`classifier.py`) — a single regex pass assigns
   one of eight categories in microseconds. A prompt that matches nothing falls
   back to the most general handler rather than failing, so classification can
   never crash a task.

2. **Deterministic solvers** (`solvers.py`) — logic assignment puzzles
   (transitive ordering, syllogisms, zebra-style grids) and pure arithmetic are
   answered by plain code with certainty. Every solver self-gates and defers to
   a model on the slightest ambiguity, so it can never turn a gettable task into
   a wrong answer.

3. **Bundled local model** (`local.py`) — a `gemma-4-E2B-it` GGUF (Q3\_K\_M,
   ~2.5 GB) is baked into the image and run on CPU with `llama.cpp`. It answers
   **math, sentiment, NER, and summarization** — four of the eight categories —
   at **zero Fireworks tokens**. A **bounded local-time budget**
   (`LOCAL_BUDGET_S`) keeps this safe on any hardware: local generation gets a
   fixed wall-clock allowance, and once it is spent the remaining tasks route
   to Fireworks instead. A fast box keeps everything local; a slow box sheds
   the overflow — so the container **never times out**, it just trades a few
   tokens for a guaranteed finish.

4. **Fireworks escalation** (`agent.py` + `llm.py`) — only **factual, code, and
   logic** (plus anything the local model returns blank on or runs out of
   budget for) reach the paid API. Each category carries a terse system prompt
   and a token cap; the `cheap` / `strong` / `code` tiers are inferred at
   runtime from whatever IDs arrive in `ALLOWED_MODELS`, never hardcoded.
   `reasoning_effort=none` suppresses hidden thinking tokens (which are
   scored), a blank or failed answer retries once on the other tier so a
   zero-scoring empty reply never ships, and **same-category batching** answers
   multiple sentiment/factual tasks in one call — the system prompt is paid
   once, not once per task.

`main.py` reads `/input/tasks.json`, runs the layers above, and writes
`/output/results.json` with every `task_id` echoed exactly. It is defensive by
design: a skeleton result file is written **before** the model loads, every
answer is flushed to disk as it lands, a SIGTERM handler flushes and exits
cleanly, and a global wall-clock ceiling keeps the local loop and the Fireworks
pool from ever summing past the harness's kill time. Whatever happens, a valid,
scoreable `results.json` is on disk.

## Why gemma-4-E2B

The 4 GB / 2 vCPU / CPU-only judging box is unforgiving: a model must be small
enough to fit and *fast* enough to actually finish work locally, yet accurate
enough to pass the gate. We benchmarked **16 small models across two rounds**
(gemma-2-2b, gemma-4-E2B, several Qwen2.5 / Qwen3 sizes, Qwen-Coder, Phi-3.5,
Llama-3.2-3B, and more) on all eight categories, measuring accuracy, output
length, and speed.

`gemma-4-E2B-it` came out **Pareto-optimal**: 100% on math, strong on the other
local categories, and — crucially — fast. Larger models (e.g. Qwen3-4B) were
slightly more accurate but too slow on 2 vCPU: the speed guard would shed their
work to the API, inflating tokens. Smaller models were faster but not accurate
enough. E2B is the sweet spot, and it keeps the project inside the Gemma family.

We also validated the whole pipeline against the organizers' **public sample
tasks** before shipping — the bundled model answers its four categories
reliably on a CPU-only 2 vCPU box, and the image exits 0 with a valid,
scoreable `results.json` under the real memory and CPU limits.

## Gemma everywhere

TokenRouter is a Gemma-first design end to end: a **local Gemma-4-E2B** does the
bulk of the work at zero cost inside the container, and Fireworks' **Gemma tier**
picks up the cheap escalations (sentiment, NER, summarization when they shed).
The cheapest token is the one you never send — and Gemma is what makes
not-sending possible here.

## Run it like the harness does

```bash
docker build -t tokenrouter .
docker run --rm --memory 4g --cpus 2 \
  -v "$PWD/sample_input:/input:ro" -v "$PWD/out:/output" \
  -e FIREWORKS_API_KEY -e FIREWORKS_BASE_URL -e ALLOWED_MODELS \
  tokenrouter
```

The image bakes in the ~2.5 GB Gemma GGUF, so the first build downloads it once.
`INCLUDE_MODEL=false` builds a weightless image that degrades gracefully to
Fireworks-only.

Local dev without Docker: `pip install -r requirements.txt`, copy `.env.example`
to `.env` and fill in your key, then
`INPUT_PATH=sample_input/tasks.json OUTPUT_PATH=out/results.json python main.py`.

## Tests

```bash
python -m unittest discover -s tests -p "test_*.py"
```

## Layout

| Path | Purpose |
|------|---------|
| `main.py` | Entrypoint: I/O contract, layered routing, deadlines, incremental flush |
| `classifier.py` | Zero-token category detection (regex) |
| `solvers.py` | Zero-token deterministic solvers (logic puzzles, arithmetic) |
| `local.py` | Bundled Gemma model, category offload, bounded local-time budget |
| `agent.py` | Per-category prompt / token-cap / tier strategy, same-category batching |
| `llm.py` | Fireworks client, tier inference, token accounting |
| `Dockerfile` | `python:3.12-slim` + `openai` + `llama-cpp-python` + bundled GGUF |
| `arsive/` | Earlier Go / local-server implementation, kept for reference |

## AMD

The agent runs on the AMD-provided judging infrastructure and does all local
inference on CPU via `llama.cpp` (no GPU required at judging time). The model
search and fine-tuning experiments behind the E2B choice were run on AMD ROCm +
Unsloth notebooks.

## Submission

The submitted image is **`ghcr.io/omerdduran/tokenrouter-track1:v17`**
(linux/amd64, public on GHCR); this README describes exactly that image. The
repository history also contains later experimental iterations (a streaming
local scheduler, deterministic answer validators) that are **not** part of the
submission. `.github/workflows/build.yml` builds and pushes on demand.
