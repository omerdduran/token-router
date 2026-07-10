"""ROCm/AMD fine-tune WITHOUT Unsloth (plain transformers + peft + trl), for
the MI300-class GPU where Unsloth's CUDA path may not apply. bf16, LoRA, no
4-bit (the GPU has plenty of memory). Produces a merged HF model; convert it
to GGUF with llama.cpp afterward (see finetune/export_gguf.sh).

    pip install --break-system-packages transformers peft trl datasets accelerate
    python train_rocm.py
"""

from __future__ import annotations

import json

import torch
from datasets import Dataset
from peft import LoraConfig, get_peft_model
from transformers import AutoModelForCausalLM, AutoTokenizer
from trl import SFTConfig, SFTTrainer

BASE = "google/gemma-2-2b-it"
DATA = "train.jsonl"
OUT = "gemma2-2b-tokenrouter-merged"

tok = AutoTokenizer.from_pretrained(BASE)
model = AutoModelForCausalLM.from_pretrained(
    BASE, torch_dtype=torch.bfloat16, device_map="auto",
    attn_implementation="eager",  # safest across backends
)

model = get_peft_model(model, LoraConfig(
    r=16, lora_alpha=16, lora_dropout=0.0, bias="none", task_type="CAUSAL_LM",
    target_modules=["q_proj", "k_proj", "v_proj", "o_proj",
                    "gate_proj", "up_proj", "down_proj"],
))

rows = []
with open(DATA, encoding="utf-8") as fh:
    for line in fh:
        line = line.strip()
        if line:
            msgs = json.loads(line)["messages"]
            rows.append({"text": tok.apply_chat_template(
                msgs, tokenize=False, add_generation_prompt=False)})
print(f"loaded {len(rows)} rows")
ds = Dataset.from_list(rows)

cfg = SFTConfig(
    dataset_text_field="text",
    max_seq_length=1024,
    per_device_train_batch_size=8,
    gradient_accumulation_steps=2,
    num_train_epochs=3,
    learning_rate=2e-4,
    bf16=True,
    warmup_ratio=0.05,
    lr_scheduler_type="cosine",
    optim="adamw_torch",
    logging_steps=10,
    output_dir="outputs",
    report_to="none",
    seed=42,
)
# trl renamed tokenizer -> processing_class in newer versions; support both.
try:
    trainer = SFTTrainer(model=model, processing_class=tok, train_dataset=ds, args=cfg)
except TypeError:
    trainer = SFTTrainer(model=model, tokenizer=tok, train_dataset=ds, args=cfg)
trainer.train()

merged = model.merge_and_unload()
merged.save_pretrained(OUT)
tok.save_pretrained(OUT)
print(f"saved merged model to {OUT}/  -> convert to GGUF with export_gguf.sh")
