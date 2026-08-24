# archie-agent image: multi-ecosystem dev environment for agent workloads.
# Pre-installs system tools agents can't install themselves (no apt, no sudo).
# Language packages are installed at runtime via uv/npm/go.
#
# Build: docker build -t archie-agent:latest .
# This image is launched only by archied with a daemon-written task brief,
# task worktree mount, and authenticated NATS environment.

# ── builder ───────────────────────────────────────────────────────────
FROM golang:1.26-trixie AS builder
ARG RUNTIME_VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags "-X github.com/samcharles93/archie-core/internal/app/agentworker.version=${RUNTIME_VERSION} -X github.com/samcharles93/archie-core/internal/installtype.buildType=container" \
    -o /usr/local/bin/archie-agent ./cmd/archie-agent/

# Same Debian release as the runtime, so glibc matches.
FROM node:24-trixie-slim AS node

# ── runtime ───────────────────────────────────────────────────────────
FROM debian:trixie-slim
ARG RUNTIME_VERSION=dev
LABEL org.opencontainers.image.version="${RUNTIME_VERSION}"

ENV DEBIAN_FRONTEND=noninteractive

# build-essential is for agents building native npm/Python extensions.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    gh \
    jq \
    ripgrep \
    unzip \
    xz-utils \
    build-essential \
    openssh-client \
    && rm -rf /var/lib/apt/lists/*

# ── Node.js 24 ────────────────────────────────────────────────────────
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/include/node /usr/local/include/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && \
    ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx && \
    npm install -g pnpm typescript tsx esbuild stylelint htmlhint && \
    npm cache clean --force

# ── Go ────────────────────────────────────────────────────────────────
# Reuse the builder's toolchain so the runtime Go can't drift from the one
# archie-agent was compiled with.
COPY --from=builder /usr/local/go /usr/local/go
ENV GOPATH="/go"
# /go/bin must be on PATH or anything `go install` puts there is unreachable.
ENV PATH="/usr/local/go/bin:/go/bin:${PATH}"

# Pinned release binaries. Building these with `go install` pulled ~2GB of
# module and build cache into the image. delve is omitted because it ships no
# prebuilt binary; `go install ...@latest` at runtime if an agent needs it.
ARG GOLANGCI_LINT_VERSION=2.12.2
ARG GOFUMPT_VERSION=0.11.0
ARG BUF_VERSION=1.72.0
RUN curl -fsSL "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64.tar.gz" \
      | tar -xz -C /usr/local/bin --strip-components=1 \
        "golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64/golangci-lint" && \
    curl -fsSL -o /usr/local/bin/gofumpt \
      "https://github.com/mvdan/gofumpt/releases/download/v${GOFUMPT_VERSION}/gofumpt_v${GOFUMPT_VERSION}_linux_amd64" && \
    curl -fsSL -o /usr/local/bin/buf \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-Linux-x86_64" && \
    chmod +x /usr/local/bin/gofumpt /usr/local/bin/buf

# ── Python via uv ─────────────────────────────────────────────────────
# uv manages its own interpreters — no apt Python, no deadsnakes PPA. One
# version is the default; agents can `uv python install <other>` on demand.
ARG PYTHON_VERSION=3.14
RUN curl -fsSL https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.local/bin:${PATH}"
RUN uv python install --default "${PYTHON_VERSION}" && rm -rf /root/.cache/uv

COPY --from=builder /usr/local/bin/archie-agent /usr/local/bin/archie-agent

# The daemon injects NATS_URL and, when configured, NATS_TOKEN.
ENTRYPOINT ["archie-agent"]
