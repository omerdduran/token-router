# Fine-tuning the bundled local model (v12)

Goal: replace the vanilla `gemma-2-2b-it` with a version fine-tuned on our task
distribution, so it reliably answers **more** categories locally (at zero
Fireworks tokens) — not just summarization + NER.

## Pipeline

1. **Generate data** (done on this machine, no GPU): a workflow produced
   `sft_raw.jsonl` — a large, verified set of `{category, prompt, answer}`
   examples across all 8 categories.

2. **Format** (no GPU):
   ```bash
   python finetune/format_sft.py sft_raw.jsonl finetune/train.jsonl
   ```
   Wraps each example in the exact chat shape the container sends the local
   model (`{system}\n\n{prompt}` → terse answer), so training matches inference.

3. **Train + export GGUF** (AMD GPU — "Unsloth + llama.cpp for Radeon" notebook):
   copy `train.jsonl` and `train_unsloth.py` into the notebook, then:
   ```bash
   python train_unsloth.py
   ```
   LoRA fine-tune of `gemma-2-2b-it`, 3 epochs, then `save_pretrained_gguf`
   exports `gemma2-2b-tokenrouter/*.gguf` (Q4_K_M) via llama.cpp.

4. **Bundle + measure** (back on this machine): point the image's `MODEL_URL`
   at the new GGUF, rebuild, and test which categories the tuned model now
   answers reliably — then widen `LOCAL_CATEGORIES` accordingly and ship v12.

## Why gemma-2-2b-it as the base

It is exactly the model the container already runs (llama-cpp-python, Q4 GGUF,
fits 4GB/2vCPU). The fine-tuned GGUF is a drop-in replacement — no runtime
changes, just a better model that offloads more work off Fireworks.
