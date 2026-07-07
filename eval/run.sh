#!/usr/bin/env bash
# Eval harness: runs the agent against eval/tasks.json using a local
# llama-server. Grading happens afterwards (Claude judges results.json
# against expected.json).
#
# Usage:
#   eval/run.sh                # assumes llama-server already running on :8080
#   MODEL=models/gemma-4-E4B-it-UD-Q4_K_XL.gguf eval/run.sh   # spawns server
set -euo pipefail
cd "$(dirname "$0")/.."

OUT_DIR="${OUT_DIR:-eval/out}"
mkdir -p "$OUT_DIR"

SERVER_PID=""
if [[ -n "${MODEL:-}" ]]; then
  llama-server -m "$MODEL" --port 8080 --host 127.0.0.1 \
    -c "${CTX:-16384}" --parallel "${PARALLEL:-4}" --no-webui >"$OUT_DIR/llama-server.log" 2>&1 &
  SERVER_PID=$!
  trap '[[ -n "$SERVER_PID" ]] && kill "$SERVER_PID" 2>/dev/null || true' EXIT
  echo "waiting for llama-server (pid $SERVER_PID)..."
  for _ in $(seq 1 120); do
    curl -sf http://127.0.0.1:8080/health >/dev/null 2>&1 && break
    sleep 1
  done
fi

START=$(date +%s)
INPUT_PATH=eval/tasks.json OUTPUT_PATH="$OUT_DIR/results.json" \
  LOCAL_BASE_URL=http://127.0.0.1:8080 \
  ${FIREWORKS_BASE_URL:+FIREWORKS_BASE_URL=$FIREWORKS_BASE_URL} \
  ${FIREWORKS_API_KEY:+FIREWORKS_API_KEY=$FIREWORKS_API_KEY} \
  ${ALLOWED_MODELS:+ALLOWED_MODELS=$ALLOWED_MODELS} \
  ${DISABLE_REMOTE:+DISABLE_REMOTE=$DISABLE_REMOTE} \
  go run ./cmd/agent 2> >(tee "$OUT_DIR/trace.log" >&2)
echo "elapsed: $(( $(date +%s) - START ))s"
echo "results: $OUT_DIR/results.json  trace: $OUT_DIR/trace.log"
