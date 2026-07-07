# Fireworks-only agent: the image is just the Go binary plus python3 for
# executing/verifying generated code. No model weights (organizer ruling:
# local models cannot be bundled; all scored inference goes through the
# Fireworks API).
#   docker buildx build --platform linux/amd64 -t <registry>/token-router:latest --push .

FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /agent ./cmd/agent

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3-minimal ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && ln -sf /usr/bin/python3 /usr/local/bin/python3
COPY --from=build /agent /usr/local/bin/agent
ENTRYPOINT ["/usr/local/bin/agent"]
