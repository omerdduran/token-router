# Judging VM runs linux/amd64. Build and push that platform explicitly:
#   docker buildx build --platform linux/amd64 -t <registry>:latest --push .
FROM python:3.12-slim

WORKDIR /app

RUN pip install --no-cache-dir "openai>=1.30.0"

COPY main.py agent.py classifier.py llm.py solvers.py ./

# Harness mounts /input and /output and injects FIREWORKS_* at run time.
ENTRYPOINT ["python", "main.py"]
