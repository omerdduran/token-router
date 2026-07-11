---
theme: default
colorSchema: dark
class: text-center
highlighter: shiki
lineNumbers: false
transition: slide-left
mdc: true
title: TokenRouter — the local-first routing agent
info: |
  TokenRouter: a token-minimizing hybrid routing agent.
drawings:
  persist: false
---

<div class="glow w-100 h-100 bg-orange-500 -top-20 -right-20"></div>
<div class="glow w-80 h-80 bg-blue-600 -bottom-24 -left-16"></div>

<div class="kicker mb-6">AMD Developer Hackathon · ACT II · Track 1</div>

<h1 class="no-bar" style="font-size:5rem; line-height:1.05;">
<span class="num-hot">Token</span><span class="num-warm">Router</span>
</h1>

<div class="text-2xl mt-2 font-light tracking-wide">the local-first routing agent</div>

<div class="pt-10 text-xl opacity-70">
The cheapest token is the one you never send.
</div>

---
layout: center
class: text-center centered-h
---

<div class="glow w-90 h-90 bg-orange-500 top-10 -left-30"></div>

# Eight task categories.<br><span class="num">Five</span> of them never touch the API.

<div v-click class="mt-10 text-lg opacity-90 max-w-170 mx-auto leading-relaxed">

Math, sentiment, NER, summarization, facts, code, logic — TokenRouter answers
them all, but treats every API token as a **purchase**. A **bundled Gemma model**
answers five of the eight categories *inside the container for free*; only the
hard remainder is ever bought.

</div>

<div v-click class="card card-accent inline-block mt-10 px-8">
<div class="text-sm opacity-70 mb-1">Track 1 is scored on total Fireworks tokens</div>
<div class="text-2xl font-bold"><span class="num">answer free first · pay only for the rest</span></div>
<div class="text-sm opacity-70 mt-1">5 of 8 categories at zero scored tokens</div>
</div>

---

<div class="kicker">Architecture</div>

# Zero-token layers first

<div class="grid mt-10 items-stretch" style="grid-template-columns: 1fr auto 1fr auto 1fr; gap: 0;">

<div class="card" style="border-color: rgba(255,122,26,0.55);">
<div class="kicker mb-2">Layer 0-1 · 0 tokens</div>
<div class="font-bold text-lg mb-3">Classify + solve</div>
<div class="text-sm leading-relaxed opacity-90">
regex router<br>
→ local model on a miss<br>
logic-puzzle solvers<br>
arithmetic evaluator
</div>
</div>

<div class="flex flex-col justify-center px-3 text-center">
<div class="text-2xl num">→</div>
<div class="text-[0.65rem] opacity-60 mt-1">not<br>solved</div>
</div>

<div class="card" style="border-color: rgba(255,122,26,0.55);">
<div class="kicker mb-2">Layer 2 · 0 tokens</div>
<div class="font-bold text-lg mb-3">Bundled Gemma 4 E2B</div>
<div class="text-sm leading-relaxed opacity-90">
math · sentiment · NER<br>
summarization · factual<br>
CPU, in the image<br>
adaptive speed guard
</div>
</div>

<div class="flex flex-col justify-center px-3 text-center">
<div class="text-2xl num">→</div>
<div class="text-[0.65rem] opacity-60 mt-1">code<br>logic</div>
</div>

<div class="card" style="border-color: rgba(100,116,139,0.7); background: rgba(148,163,184,0.06);">
<div class="kicker mb-2" style="color:#94a3b8;">Layer 3 · minimal spend</div>
<div class="font-bold text-lg mb-3">Fireworks API</div>
<div class="text-sm leading-relaxed opacity-90">
terse prompts · tight caps<br>
reasoning_effort: none<br>
tiers from ALLOWED_MODELS<br>
blank → retry on other tier
</div>
</div>

</div>

<div class="flex items-center justify-center gap-4 mt-8">
<div class="px-4 py-1 rounded-full text-sm font-bold" style="background:#ff7a1a; color:#14100a;">task</div>
<div class="flex-1 border-t border-dashed" style="border-color: rgba(255,255,255,0.25); max-width: 18rem;"></div>
<div class="text-sm opacity-70">the first three layers cost <b>nothing</b></div>
<div class="flex-1 border-t border-dashed" style="border-color: rgba(255,255,255,0.25); max-width: 18rem;"></div>
<div class="px-4 py-1 rounded-full text-sm font-bold" style="background:#fbbf24; color:#14100a;">answer</div>
</div>

<div class="mt-6 text-center opacity-60 text-sm">
Only what survives to the last layer spends a token.
</div>

---
layout: two-cols-header
---

<div class="kicker">Layers 0–1</div>

# The free floor

::left::

<div class="text-[0.92rem] pr-6">

**Zero-token classification** — a regex pass assigns one of eight categories.
When nothing matches — the brittle spot on reworded prompts — the bundled model
picks the category *semantically*, still for free.

**Deterministic solvers** — plain Python, microseconds:

- Arithmetic expression evaluator
- Logic assignment puzzles: orderings, syllogisms, zebra grids

</div>

::right::

<div class="text-sm opacity-70 mb-2">every solver self-gates</div>

<div class="card card-accent mt-2 text-sm">
An unparsed clue or an ambiguous solution means <i>defer</i>, never guess — so a
solver can <b>never</b> turn a gettable task into a wrong answer.
<br><br>
A deferral costs a few tokens. A wrong answer costs the accuracy gate.
</div>

<div v-click class="card mt-4 text-sm opacity-90">
Semantic fallback = "routing that survives messy prompts", using the model we
already ship — no second runtime, no extra tokens.
</div>

---
layout: two-cols-header
---

<div class="kicker">Layer 2</div>

# A local model that does the bulk of the work

::left::

<div class="text-[0.92rem] pr-6">

A **Gemma 4 E2B** (Q3 GGUF, llama.cpp) ships *inside* the image — sized for the
4 GB / 2 vCPU / CPU-only grading box.

It answers **five of the eight categories** at **zero Fireworks tokens**:

- math · sentiment · NER · summarization · factual

Local inference counts toward accuracy and **zero** toward the token score.

</div>

::right::

<div v-click class="card card-accent text-sm">

**Adaptive speed guard:** a startup warmup measures this box's tokens/sec, and a
task is kept local only if its estimated time fits the budget.

Fast box → everything local. Slow box → long work sheds to Fireworks. Either
way: <b>no TIMEOUT</b>.

</div>

<div v-click class="card mt-4 text-sm opacity-90">
Tuned to the real tasks: factual = explanatory common knowledge, sentiment
reviews are <i>mixed</i> (name both sides), summaries are strictly formatted.
</div>

---

<div class="kicker">Layer 3</div>

# When we do pay, we pay the minimum

<div class="grid grid-cols-3 gap-5 mt-8">

<div v-click class="card text-center">
<div class="text-4xl num">2 / 8</div>
<div class="mt-2 text-sm opacity-80">categories ever reach the API — only
<b>code</b> and <b>logic</b>; everything else was answered for free</div>
</div>

<div v-click class="card text-center">
<div class="text-4xl num">effort: none</div>
<div class="mt-2 text-sm opacity-80"><code>reasoning_effort: none</code> suppresses
hidden thinking tokens, which are billed like any others</div>
</div>

<div v-click class="card text-center">
<div class="text-4xl num">1 retry</div>
<div class="mt-2 text-sm opacity-80">a blank or failed reply retries once on the
other tier — an empty, zero-scoring answer never ships</div>
</div>

</div>

<div v-click class="mt-8 text-center opacity-70 text-sm">
tiers (cheap / strong / code) inferred at runtime from <code>ALLOWED_MODELS</code> — never hardcoded
</div>

---
layout: center
class: text-center centered-h
---

<div class="glow w-100 h-100 bg-orange-500 -top-10 right-0"></div>

<div class="kicker">The result</div>

<h1 class="no-bar"><span class="num" style="font-size:6.5rem; line-height:1;">5 / 8</span></h1>

<div class="text-xl mt-1">categories answered at <b>zero Fireworks tokens</b></div>

<div class="grid grid-cols-3 gap-6 mt-12 text-center">

<div class="card">
<div class="text-3xl num">16</div>
<div class="text-sm opacity-75 mt-1">small models benchmarked<br>to pick the engine</div>
</div>

<div class="card">
<div class="text-3xl num">no TIMEOUT</div>
<div class="text-sm opacity-75 mt-1">hardware-adaptive routing<br>on the 4 GB / 2 vCPU box</div>
</div>

<div class="card">
<div class="text-3xl num">public samples</div>
<div class="text-sm opacity-75 mt-1">validated against the<br>organizers' sample set</div>
</div>

</div>

<div class="mt-8 text-xs opacity-50">
the live leaderboard is noisy while catching up — this deck states what the system does, not a rank
</div>

---
layout: two-cols-header
---

<div class="kicker">Engineering culture</div>

# We measured 16 models. E2B won on evidence.

::left::

<div class="pr-8 text-[0.92rem]">

The 4 GB / 2 vCPU / CPU box is unforgiving: a model must **fit**, be **fast
enough to finish locally**, *and* be **accurate enough** to pass.

We benchmarked **16 small models over two rounds** — gemma-2-2b, gemma-4-E2B,
several Qwen2.5 / Qwen3 sizes, Qwen-Coder, Phi-3.5, Llama-3.2-3B — on all eight
categories.

</div>

::right::

<div v-click class="text-[0.92rem]">

| Model | fits | fast | accurate |
|---|---|---|---|
| gemma-2-2b | ✅ | ✅ | weak |
| **gemma-4-E2B** | ✅ | ✅ | **strong** |
| Qwen3-4B | ✅ | <span class="text-red-400 font-bold">no</span> | strong |
| Qwen3-1.7B | ✅ | ✅ | drops off |

</div>

<div v-click class="card card-accent mt-4 text-sm">
<b>Lesson:</b> the bigger models were more accurate but too slow on 2 vCPU — the
speed guard would just push their work back to the API. <b>E2B is Pareto-optimal.</b>
</div>

---

<div class="kicker">Robustness</div>

# It cannot crash, stall, or emit bad JSON

<div class="grid grid-cols-2 gap-x-8 gap-y-3 mt-6 text-[0.9rem]">

<div class="card">📄 <b>Skeleton <code>results.json</code> before the model loads</b> — even an OOM leaves valid, scorable JSON</div>
<div class="card">💾 <b>Incremental flush</b> — every answer is on disk the moment it lands</div>
<div class="card">🛑 <b>SIGTERM flush + hard exit</b> — an early kill still submits everything answered so far</div>
<div class="card">🧯 <b>Per-task isolation</b> — one adversarial prompt can't take down the run</div>
<div class="card">⏱️ <b>Global deadline ceiling</b> — local loop + API pool never sum past the kill time</div>
<div class="card">🔻 <b>Graceful degrade</b> — any local failure falls back to Fireworks-only</div>

</div>

<div v-click class="card card-accent mt-6 text-center">
Verified under the real limits:<br>
<code>--memory 4g --cpus 2</code> → model loads, categories answered locally at 0 tokens, exit 0.
</div>

---
layout: center
class: text-center centered-h
---

<div class="glow w-90 h-90 bg-orange-500 bottom-0 -right-20"></div>

<div class="kicker">Best use of Gemma</div>

# Gemma, everywhere

<div class="mt-8 text-xl leading-relaxed">

**Gemma 4 E2B** answers locally for free —<br>
chosen over 15 other small models *on measured evidence*.

<div class="mt-4"></div>

**Fireworks Gemma** is available for the escalations —<br>
with thinking off and prompts tuned per category.

</div>

<div v-click class="mt-10 opacity-60">
one model family · local + escalation · every routing decision earned by data
</div>

---
layout: center
class: text-center centered-h
---

<div class="glow w-110 h-110 bg-orange-500 -top-30 left-1/3"></div>

<h1 class="no-bar" style="font-size:4rem;"><span class="num-hot">Token</span><span class="num-warm">Router</span></h1>

<div class="text-2xl mt-2 opacity-90">
Answer for free — or buy the minimum.
</div>

<div class="mt-12 text-sm opacity-70 leading-loose">

**Python** orchestrator · **llama.cpp** + Gemma 4 E2B in-container · **Fireworks AI** escalation<br>
5 of 8 categories at zero tokens · hardware-adaptive · never times out

</div>
