"""Run a GGUF model over the held-out test set, using the EXACT inference
format the container uses, and dump its answers next to the gold answers.

Run it once per model (vanilla vs fine-tuned), then judge the two answer files
against the gold to see, per category, whether fine-tuning actually helped —
BEFORE bundling anything into the image.

    python finetune/eval_local.py <model.gguf> testset.jsonl answers.jsonl

testset.jsonl rows: {"category","prompt","answer"(gold)}
answers.jsonl rows: {"category","prompt","gold","answer"(model)}
"""

from __future__ import annotations

import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from agent import _CONFIG  # noqa: E402

SYSTEM = {cat.value: cfg[0] for cat, cfg in _CONFIG.items()}
MAX_TOKENS = {cat.value: cfg[1] for cat, cfg in _CONFIG.items()}


def run(model_path: str, test_path: str, out_path: str) -> None:
    from llama_cpp import Llama

    llm = Llama(model_path=model_path, n_ctx=2048, n_threads=4, verbose=False)
    rows = []
    with open(test_path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if line:
                rows.append(json.loads(line))

    with open(out_path, "w", encoding="utf-8") as out:
        for r in rows:
            cat = r["category"]
            prompt = r["prompt"]
            gold = r.get("answer", "")
            system = SYSTEM.get(cat, "")
            resp = llm.create_chat_completion(
                messages=[{"role": "user", "content": f"{system}\n\n{prompt}"}],
                max_tokens=MAX_TOKENS.get(cat, 256),
                temperature=0,
            )
            ans = (resp["choices"][0]["message"]["content"] or "").strip()
            out.write(json.dumps({"category": cat, "prompt": prompt,
                                  "gold": gold, "answer": ans},
                                 ensure_ascii=False) + "\n")
    print(f"ran {len(rows)} test items -> {out_path}")


if __name__ == "__main__":
    if len(sys.argv) != 4:
        print("usage: eval_local.py <model.gguf> <testset.jsonl> <answers.jsonl>",
              file=sys.stderr)
        sys.exit(1)
    run(sys.argv[1], sys.argv[2], sys.argv[3])
