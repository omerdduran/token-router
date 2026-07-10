# Judging VM: linux/amd64, 4GB RAM / 2 vCPU, CPU-only, image <= 10GB.
#   docker buildx build --platform linux/amd64 -t <registry>:<tag> --push .
#
# A small GGUF (gemma-2-2b-it, ~1.6GB) is baked in and answers the categories
# it handles reliably (summarization, NER) at zero Fireworks tokens; everything
# else goes to Fireworks. INCLUDE_MODEL=false ships a weightless image that
# degrades to Fireworks-only.
ARG INCLUDE_MODEL=true

# --- Model weights ---
FROM alpine:3.20 AS model-true
ARG MODEL_URL=https://huggingface.co/bartowski/gemma-2-2b-it-GGUF/resolve/main/gemma-2-2b-it-Q4_K_M.gguf
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

# Local model handles summarization + NER (measured reliable on a 2B); the
# grading box has 2 vCPU, so llama.cpp runs on 2 threads.
ENV LOCAL=true \
    LOCAL_MODEL_PATH=/models/model.gguf \
    LOCAL_CATEGORIES=summarization,ner \
    LOCAL_CTX_SIZE=4096 \
    LOCAL_THREADS=2

# Harness mounts /input and /output and injects FIREWORKS_* at run time.
ENTRYPOINT ["python", "main.py"]
