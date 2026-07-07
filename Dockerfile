# Build for the judging VM: linux/amd64, compressed image must stay under 10GB.
#   docker buildx build --platform linux/amd64 -t <registry>/token-router:latest --push .

# --- Stage 1: Go binary ---
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /agent ./cmd/agent

# --- Stage 2: model weights ---
FROM alpine:3.20 AS model
ARG MODEL_URL=https://huggingface.co/unsloth/gemma-4-E4B-it-GGUF/resolve/main/gemma-4-E4B-it-UD-Q4_K_XL.gguf
RUN apk add --no-cache curl && curl -fL --retry 3 -o /model.gguf "$MODEL_URL"

# --- Stage 3: runtime = official llama.cpp server image (CPU) + python3 ---
FROM ghcr.io/ggml-org/llama.cpp:server
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /agent /usr/local/bin/agent
COPY --from=model /model.gguf /models/model.gguf

ENV LOCAL_MODEL_PATH=/models/model.gguf \
    LOCAL_SERVER_BIN=/app/llama-server \
    LOCAL_BASE_URL=http://127.0.0.1:8080

ENTRYPOINT ["/usr/local/bin/agent"]
