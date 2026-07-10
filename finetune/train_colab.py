"""Robust Colab (T4) fine-tune that bypasses trl + datasets entirely — plain
transformers.Trainer with manual tokenization, so none of the trl 0.24 /
datasets pickle issues apply. Model load + GGUF export still via Unsloth.

Run after: pip install unsloth ; and train.jsonl next to this file.
    python train_colab.py
"""

from __future__ import annotations

import json

from torch.utils.data import Dataset
from transformers import DataCollatorForSeq2Seq, Trainer, TrainingArguments
from unsloth import FastLanguageModel

model, tokenizer = FastLanguageModel.from_pretrained(
    "unsloth/gemma-2-2b-it", max_seq_length=1024, load_in_4bit=True)
model = FastLanguageModel.get_peft_model(
    model, r=16, lora_alpha=16, lora_dropout=0.0,
    target_modules=["q_proj", "k_proj", "v_proj", "o_proj",
                    "gate_proj", "up_proj", "down_proj"],
    use_gradient_checkpointing="unsloth", random_state=42)

if tokenizer.pad_token is None:
    tokenizer.pad_token = tokenizer.eos_token

rows = [json.loads(line) for line in open("train.jsonl", encoding="utf-8") if line.strip()]
examples = []
for r in rows:
    text = tokenizer.apply_chat_template(r["messages"], tokenize=False,
                                         add_generation_prompt=False)
    ids = tokenizer(text, truncation=True, max_length=1024,
                    add_special_tokens=False)["input_ids"]
    examples.append({"input_ids": ids, "attention_mask": [1] * len(ids), "labels": list(ids)})
print(f"tokenized {len(examples)} examples")


class DS(Dataset):
    def __init__(self, e):
        self.e = e

    def __len__(self):
        return len(self.e)

    def __getitem__(self, i):
        return self.e[i]


FastLanguageModel.for_training(model)
trainer = Trainer(
    model=model, train_dataset=DS(examples),
    data_collator=DataCollatorForSeq2Seq(tokenizer, padding=True),
    args=TrainingArguments(
        output_dir="outputs", per_device_train_batch_size=2,
        gradient_accumulation_steps=4, num_train_epochs=3, learning_rate=2e-4,
        fp16=True, logging_steps=10, lr_scheduler_type="cosine",
        warmup_steps=20, optim="adamw_8bit", seed=42, report_to="none",
        save_strategy="no"))
trainer.train()

model.save_pretrained_gguf("gemma2-2b-tokenrouter", tokenizer,
                           quantization_method="q4_k_m")
print("DONE -> gemma2-2b-tokenrouter/ has the .gguf")
