# Judging VM runs linux/amd64. Build and push that platform explicitly:
#   docker buildx build --platform linux/amd64 -t <registry>:latest --push .
FROM python:3.12-slim

WORKDIR /app

RUN pip install --no-cache-dir "openai>=1.30.0"

COPY main.py agent.py classifier.py llm.py solvers.py ./

# Pin the leanest allowed model (per-model tokenizers bill the same text
# differently; deepseek-v4-pro measured lowest). Guarded in llm.py: ignored
# if not present in the harness's ALLOWED_MODELS. Override at build time with
#   --build-arg PREFERRED_MODEL=...
ARG PREFERRED_MODEL=deepseek-v4-pro
ENV PREFERRED_MODEL=${PREFERRED_MODEL}

# Harness mounts /input and /output and injects FIREWORKS_* at run time.
ENTRYPOINT ["python", "main.py"]
