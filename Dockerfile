# archie-agent fat image: multi-ecosystem development environment for
# generic agent workloads. Pre-installs system-level tools that agents
# can't easily install themselves (no apt, no sudo). Language-specific
# packages are installed by the agent at runtime via UV/npm/go.
#
# Build: docker build -t archie-agent:latest .
# Run:   docker run -e NATS_URL=nats://host:4222 archie-agent:latest

# ── builder ───────────────────────────────────────────────────────────
FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /usr/local/bin/archie-agent ./cmd/archie-agent/

# ── runtime ───────────────────────────────────────────────────────────
FROM ubuntu:24.04

# Avoid interactive prompts during package installs.
ENV DEBIAN_FRONTEND=noninteractive

# ── system packages ───────────────────────────────────────────────────
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    gh \
    jq \
    unzip \
    xz-utils \
    build-essential \
    openssh-client \
    && rm -rf /var/lib/apt/lists/*

# ── Go ────────────────────────────────────────────────────────────────
# Latest stable Go for agent-driven Go tasks (build, test, lint).
ARG GO_VERSION=1.26.2
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" \
    | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH="/go"

# Pre-install common Go tools that need a Go toolchain.
RUN go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest && \
    go install mvdan.cc/gofumpt@latest && \
    rm -rf /go/pkg/mod/cache

# ── Node.js 22 LTS ────────────────────────────────────────────────────
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y --no-install-recommends nodejs && \
    rm -rf /var/lib/apt/lists/*
RUN npm install -g pnpm typescript tsx esbuild stylelint htmlhint && \
    npm cache clean --force

# ── Python + UV ───────────────────────────────────────────────────────
# Python 3.14 (latest) and 3.13 via deadsnakes PPA.
RUN apt-get update && apt-get install -y --no-install-recommends \
    software-properties-common && \
    add-apt-repository -y ppa:deadsnakes/ppa && \
    apt-get install -y --no-install-recommends \
    python3.14 python3.14-dev python3.14-venv \
    python3.13 python3.13-dev python3.13-venv \
    && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.local/bin:${PATH}"

# ── archie-agent ──────────────────────────────────────────────────────
COPY --from=builder /usr/local/bin/archie-agent /usr/local/bin/archie-agent

# ── entrypoint ────────────────────────────────────────────────────────
# The daemon passes -nats-url and other flags at container start.
ENTRYPOINT ["archie-agent"]
