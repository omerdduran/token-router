#!/usr/bin/env bash
# Convert the merged HF model (from train_rocm.py) to a Q4_K_M GGUF using
# llama.cpp, then you hand the .gguf back to bundle into the image.
#
#   bash export_gguf.sh gemma2-2b-tokenrouter-merged
set -euo pipefail

SRC="${1:-gemma2-2b-tokenrouter-merged}"
OUT_F16="model-f16.gguf"
OUT_Q4="gemma2-2b-tokenrouter-Q4_K_M.gguf"

if [ ! -d llama.cpp ]; then
  git clone --depth 1 https://github.com/ggml-org/llama.cpp
fi
pip install --break-system-packages -q -r llama.cpp/requirements.txt || true

# HF -> GGUF (f16)
python llama.cpp/convert_hf_to_gguf.py "$SRC" --outfile "$OUT_F16" --outtype f16

# Build the quantizer if not present, then quantize to Q4_K_M
if [ ! -x llama.cpp/build/bin/llama-quantize ]; then
  cmake -S llama.cpp -B llama.cpp/build -DGGML_NATIVE=OFF >/dev/null
  cmake --build llama.cpp/build --target llama-quantize -j"$(nproc)" >/dev/null
fi
llama.cpp/build/bin/llama-quantize "$OUT_F16" "$OUT_Q4" Q4_K_M

echo "done -> $OUT_Q4  (hand this file back to bundle into the image)"
