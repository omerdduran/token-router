"""Fine-tune gemma-2-2b-it with Unsloth (LoRA) on the SFT dataset, then export
a Q4_K_M GGUF for the container to bundle.

Run this in the AMD "Unsloth + llama.cpp for Radeon" notebook after copying
finetune/train.jsonl next to it.

    python train_unsloth.py            # trains + exports GGUF

Output: gemma2-2b-tokenrouter/  (contains *.gguf). Upload the .gguf somewhere
the Docker build can curl it, or hand it back to bundle into the image.

gemma-2-2b-it is the base on purpose: it is exactly the model the container
already runs, so the tuned GGUF is a drop-in replacement that still loads in
llama-cpp-python and fits the 4GB/2vCPU judging box.
"""

from __future__ import annotations

import json

from datasets import Dataset
from trl import SFTConfig, SFTTrainer
from unsloth import FastLanguageModel

MAX_SEQ_LEN = 2048
DATA_PATH = "train.jsonl"
OUT_DIR = "gemma2-2b-tokenrouter"
GGUF_QUANT = "q4_k_m"  # matches the container's current model


def load_dataset(tokenizer) -> Dataset:
    rows = []
    with open(DATA_PATH, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            msgs = json.loads(line)["messages"]
            rows.append({"text": tokenizer.apply_chat_template(
                msgs, tokenize=False, add_generation_prompt=False)})
    print(f"loaded {len(rows)} training rows")
    return Dataset.from_list(rows)


def main() -> None:
    model, tokenizer = FastLanguageModel.from_pretrained(
        model_name="unsloth/gemma-2-2b-it",
        max_seq_length=MAX_SEQ_LEN,
        load_in_4bit=True,
    )
    model = FastLanguageModel.get_peft_model(
        model,
        r=16,
        lora_alpha=16,
        lora_dropout=0.0,
        target_modules=["q_proj", "k_proj", "v_proj", "o_proj",
                        "gate_proj", "up_proj", "down_proj"],
        use_gradient_checkpointing="unsloth",
        random_state=42,
    )

    dataset = load_dataset(tokenizer)

    cfg = SFTConfig(
        dataset_text_field="text",
        dataset_num_proc=1,  # avoid a multiprocessing pickle error in datasets.map
        fp16=True,  # T4 is Turing: fp16 only (bf16 needs Ampere+)
        per_device_train_batch_size=2,
        gradient_accumulation_steps=4,
        warmup_ratio=0.05,
        num_train_epochs=3,
        learning_rate=2e-4,
        logging_steps=10,
        optim="adamw_8bit",
        weight_decay=0.01,
        lr_scheduler_type="cosine",
        seed=42,
        output_dir="outputs",
        report_to="none",
    )
    # trl renamed tokenizer -> processing_class in newer versions; support both.
    try:
        trainer = SFTTrainer(model=model, processing_class=tokenizer,
                             train_dataset=dataset, args=cfg)
    except TypeError:
        trainer = SFTTrainer(model=model, tokenizer=tokenizer,
                             train_dataset=dataset, args=cfg)
    trainer.train()

    # Merge LoRA + export GGUF via llama.cpp (bundled with Unsloth).
    model.save_pretrained_gguf(OUT_DIR, tokenizer, quantization_method=GGUF_QUANT)
    print(f"exported GGUF under {OUT_DIR}/")


if __name__ == "__main__":
    main()
