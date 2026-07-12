# Judging VM: linux/amd64, 4GB RAM / 2 vCPU, CPU-only, image <= 10GB.
#   docker buildx build --platform linux/amd64 -t <registry>:<tag> --push .
#
# A small GGUF (gemma-4-E2B-it, Q3_K_M, ~2.54GB) is baked in and answers ALL
# eight categories at zero Fireworks tokens (benchmarked well above the 50%
# accuracy gate on each); Fireworks is only the fallback for tasks that shed
# (blank output, or won't fit before the streaming hard stop).
# gemma-4-E2B was picked by benchmarking 16 small models: it is Pareto-optimal
# for this 4GB/2vCPU/CPU box (fast enough to finish work locally, accurate
# enough to pass). Q3 fits the 4GB box with ~1GB of headroom. INCLUDE_MODEL=false
# ships a weightless image that degrades to Fireworks-only.
ARG INCLUDE_MODEL=true

# --- Model weights ---
FROM alpine:3.20 AS model-true
ARG MODEL_URL=https://huggingface.co/unsloth/gemma-4-E2B-it-GGUF/resolve/main/gemma-4-E2B-it-Q3_K_M.gguf
# --http1.1: HF's CDN intermittently resets HTTP/2 streams on large pulls;
# retry-all-errors + resume make the download deterministic.
RUN apk add --no-cache curl && \
    curl -fL --http1.1 --retry 5 --retry-all-errors -C - -o /model.gguf "$MODEL_URL"

FROM alpine:3.20 AS model-false
RUN touch /model.gguf

FROM model-${INCLUDE_MODEL} AS model

# --- Runtime ---
FROM python:3.12-slim
WORKDIR /app

RUN pip install --no-cache-dir "openai>=1.30.0" && \
    pip install --no-cache-dir llama-cpp-python \
      --extra-index-url https://abetlen.github.io/llama-cpp-python/whl/cpu

COPY --from=model /model.gguf /models/model.gguf
COPY main.py agent.py classifier.py llm.py solvers.py local.py ./

# The local model answers all eight categories; any shed escalates to
# Fireworks. The grading box has 2 vCPU, so llama.cpp runs on 2 threads.
# n_ctx=2048 keeps the KV cache small enough to stay well inside 4GB RAM
# alongside the model, the agent, and Fireworks work. Local generation streams
# with a hard deadline (interruptible) and runs to within REMOTE_RESERVE_S
# (default 45s) of the global ceiling; the measured-speed guard in
# local.py/main.py sheds work that won't fit (no TIMEOUT).
ENV LOCAL=true \
    LOCAL_MODEL_PATH=/models/model.gguf \
    LOCAL_CATEGORIES=factual,math,sentiment,summarization,ner,code_debug,code_gen,logic \
    LOCAL_CTX_SIZE=2048 \
    LOCAL_THREADS=2 \
    BATCH=true

# Harness mounts /input and /output and injects FIREWORKS_* at run time.
ENTRYPOINT ["python", "main.py"]
