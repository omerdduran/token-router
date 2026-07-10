"""Turn the generated (category, prompt, answer) examples into the exact chat
format the container sends the local model at inference time, so the fine-tuned
model learns to answer OUR prompts in OUR format.

Inference (local.py) sends one user turn: f"{system}\\n\\n{prompt}" and expects
the terse answer back. Training must match that shape exactly.

    python finetune/format_sft.py sft_raw.jsonl finetune/train.jsonl
"""

from __future__ import annotations

import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from agent import _CONFIG  # noqa: E402  (needs repo root on sys.path)

# category value -> the system instruction the container actually sends
SYSTEM = {cat.value: cfg[0] for cat, cfg in _CONFIG.items()}


def main(in_path: str, out_path: str) -> None:
    rows = []
    with open(in_path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if line:
                rows.append(json.loads(line))

    kept = 0
    with open(out_path, "w", encoding="utf-8") as out:
        for r in rows:
            cat = r.get("category")
            prompt = (r.get("prompt") or "").strip()
            answer = (r.get("answer") or "").strip()
            system = SYSTEM.get(cat)
            if not (system and prompt and answer):
                continue
            user = f"{system}\n\n{prompt}"
            out.write(json.dumps({"messages": [
                {"role": "user", "content": user},
                {"role": "assistant", "content": answer},
            ]}, ensure_ascii=False) + "\n")
            kept += 1
    print(f"wrote {kept} training rows to {out_path}")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print("usage: format_sft.py <raw.jsonl> <train.jsonl>", file=sys.stderr)
        sys.exit(1)
    main(sys.argv[1], sys.argv[2])
