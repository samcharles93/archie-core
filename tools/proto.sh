#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

case "${1:-}" in
  install)
    go install github.com/bufbuild/buf/cmd/buf@v1.71.0
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
    ;;
  generate)
    (cd proto && buf generate)
    gofumpt -w internal/contracts
    ;;
  lint)
    (cd proto && buf lint)
    # A missing main ref is an error; only the initial introduction of proto
    # has no published contract to compare. Tags in CI also need this ref.
    git rev-parse --verify refs/heads/main >/dev/null
    if git cat-file -e main:proto/buf.yaml 2>/dev/null; then
      buf breaking proto --against "$(git rev-parse --git-common-dir)#branch=main,subdir=proto"
    else
      echo 'No proto baseline on main yet; checking the first contract with buf lint.'
    fi
    ;;
  check)
    bash tools/proto.sh generate
    git diff --exit-code -- internal/contracts
    if [[ -n "$(git ls-files --others --exclude-standard -- internal/contracts)" ]]; then
      echo 'Generated contracts are untracked; add them before running proto:check.' >&2
      exit 1
    fi
    ;;
  *)
    echo 'usage: bash tools/proto.sh {install|generate|lint|check}' >&2
    exit 2
    ;;
esac
