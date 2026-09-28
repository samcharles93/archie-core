#!/usr/bin/env bash
# Run the approved PR-review benchmark without posting to pull requests.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${PRBENCH_REVIEW_MODEL:?set PRBENCH_REVIEW_MODEL to a configured provider/model}"
: "${PRBENCH_CLASSIFICATION_MODEL:?set PRBENCH_CLASSIFICATION_MODEL to a configured provider/model}"
: "${PRBENCH_JUDGE_MODEL:=anthropic/claude-sonnet-4.6}"
: "${PRBENCH_OUT:=bin/prbench-results}"
if [[ -z "${GH_TOKEN:-}" && -z "${GITHUB_TOKEN:-}" ]] && command -v gh >/dev/null; then
    export GH_TOKEN="$(gh auth token)"
fi
exec go run ./cmd/archie-prbench \
    -review-model "$PRBENCH_REVIEW_MODEL" \
    -classification-model "$PRBENCH_CLASSIFICATION_MODEL" \
    -judge-model "$PRBENCH_JUDGE_MODEL" \
    -out "$PRBENCH_OUT" "$@"
