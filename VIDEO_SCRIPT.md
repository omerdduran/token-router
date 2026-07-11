# TokenRouter — Demo Video Script

**Target length:** ~2:30. **Format:** screen recording + voiceover.
Each scene lists **[VISUAL]** (what's on screen) and **[VO]** (what you say).
Keep the pace brisk; the demo in Scene 3 is the moment that sells it.

---

### Scene 1 — Hook (0:00–0:20)

**[VISUAL]** Title card: "TokenRouter — a token-efficient routing agent · AMD
Hackathon Track 1". Optionally the one-line architecture diagram from the README.

**[VO]**
> Track 1 scores you on one thing: how few Fireworks tokens you spend, while
> still answering correctly. So we built TokenRouter around a simple idea — the
> cheapest token is the one you never send. Instead of calling the API smartly,
> we avoid calling it at all whenever we can.

---

### Scene 2 — Architecture (0:20–0:50)

**[VISUAL]** The 4-layer diagram (from README/SLIDES). Highlight each layer as
you mention it — the first three glow "0 tokens", the fourth is "paid".

**[VO]**
> Every task falls through four layers. First, a zero-token classifier — regex,
> and when that's unsure, a small local model decides the category. Second,
> deterministic solvers for logic puzzles and arithmetic. Third — and this is
> the core — a Gemma model baked right into the container answers five of the
> eight categories on CPU, for free. Only what's left — code and logic — reaches
> the paid Fireworks API.

---

### Scene 3 — Live demo (0:50–1:35)  ← the key moment

**[VISUAL]** A terminal. Run the container on the public sample tasks:
```
docker run --rm --memory 4g --cpus 2 \
  -e FIREWORKS_API_KEY -e FIREWORKS_BASE_URL -e ALLOWED_MODELS \
  -v $PWD/sample_input:/input:ro -v $PWD/out:/output tokenrouter
```
Let the log scroll. Point the cursor at two lines: the startup line
`local: warmup … tok/s` and the final line
`Wrote N result(s) … tokens: total=0`. Then `cat out/results.json` and scroll
through a factual answer, a mixed-sentiment answer, and an NER answer.

**[VO]**
> Here it is on the real sample tasks. On startup it measures how fast this box
> is — that's the adaptive speed guard. Watch the token counter at the end:
> zero. Factual questions, sentiment, NER, summaries, math — all answered
> locally by Gemma, at zero API cost. And notice the sentiment answer names both
> the good and the bad side of a mixed review, which is exactly what the graders
> want.

---

### Scene 4 — Why it holds up (1:35–2:05)

**[VISUAL]** The 16-model benchmark table from SLIDES (accuracy vs speed).
Then a quick shot of the "no TIMEOUT" reliability bullet.

**[VO]**
> Why Gemma-4-E2B? Because we benchmarked sixteen small models on this exact
> four-gigabyte, two-CPU box. E2B was the sweet spot — fast enough to actually
> finish work locally, accurate enough to pass. Bigger models were more accurate
> but too slow, so the speed guard would just push their work back to the API.
> And because the routing adapts to the hardware, the container never times out:
> a slow box simply spends a few tokens instead of failing.

---

### Scene 5 — Close (2:05–2:30)

**[VISUAL]** Back to the title card, with three bullets fading in: "5 of 8
categories at zero tokens", "16-model benchmark", "Gemma everywhere". End on the
GHCR image reference.

**[VO]**
> TokenRouter is Gemma from end to end — a local Gemma doing the work for free,
> and Fireworks' Gemma tier for the hard remainder. The most token-efficient
> answer is the one you compute for free, and Gemma is what makes free possible.
> Thanks for watching.

---

## Shot list / assets to capture

- Title card (Scene 1 & 5) — architecture diagram from `README.md`.
- 4-layer diagram highlight (Scene 2).
- Terminal recording of the container run on `sample_input/tasks.json`
  showing `warmup … tok/s` and `tokens: total=0`, then `results.json` (Scene 3).
- Benchmark table screenshot from `SLIDES.md` (Scene 4).
- GHCR image reference on the closing card (Scene 5).
