# syntax=docker/dockerfile:1

# ---- build: Go toolchain + libvips headers (govips needs CGO) ----
FROM golang:1.26-trixie AS build
RUN apt-get update \
 && apt-get install -y --no-install-recommends libvips-dev pkg-config \
    # HEVC decoder for libheif; not pulled in with --no-install-recommends.
    libheif-plugin-libde265 \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ENV CGO_ENABLED=1
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot

# ---- test: `docker build --target test .` runs the full suite against real libvips ----
FROM build AS test
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go vet ./... && go test ./...

# ---- runtime ----
FROM debian:trixie-slim AS runtime
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
    libvips42t64 libheif1 libheif-plugin-libde265 ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin bot \
 && mkdir -p /data && chown bot:bot /data
COPY --from=build /out/bot /usr/local/bin/bot

# Fail the image build unless this exact runtime can decode HEIC (plus JPEG/PNG/WebP round trips).
RUN ["/usr/local/bin/bot", "-selfcheck"]

USER bot
ENV DATABASE_PATH=/data/bot.db \
    HEALTH_ADDR=:8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD ["/usr/local/bin/bot", "-healthcheck"]
ENTRYPOINT ["/usr/local/bin/bot"]
