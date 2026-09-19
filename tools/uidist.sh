#!/usr/bin/env bash
# Verify the committed dashboard bundle matches a fresh build of ui/src.
#
# ui/dist is committed because the Go build embeds it, so it is a generated
# source file: a change to ui/src that lands without the rebuilt bundle ships a
# dashboard no reviewer ever saw. Rebuilding it in place here would bless
# exactly that, silently -- and with more than one agent working in its own
# worktree, each could "pass" while producing a different bundle.
#
# So this builds somewhere temporary and compares, the rule docs:check already
# follows for docs/data/generated/contracts.json. A mismatch means ui/src
# changed without `task ui`, or the bundle was hand-edited; the fix is the same
# either way.
set -euo pipefail

cd "$(dirname "$0")/.."

case "${1:-check}" in
  check) ;;
  *)
    echo 'usage: bash tools/uidist.sh check' >&2
    exit 2
    ;;
esac

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

# The comparison only means something if the build is deterministic, so a
# failure here is staleness rather than toolchain noise. It is: vite is
# configured to emit fixed asset names (no content hashes), and rebuilding an
# unchanged tree produces byte-identical output.
if ! build_log="$(cd ui && npm run build --silent -- --outDir "$out" --emptyOutDir 2>&1)"; then
	echo "$build_log" >&2
	echo 'the dashboard does not build; fix that before checking freshness' >&2
	exit 1
fi

if ! differences="$(diff -rq ui/dist "$out" 2>&1)"; then
	cat >&2 <<EOF
ui/dist does not match a fresh build of ui/src:

$differences

Run \`task ui\` and commit the rebuilt bundle in the same commit as the change
that caused it. The Go build embeds ui/dist, so a stale bundle ships a
dashboard nobody reviewed.
EOF
	exit 1
fi

echo 'ui/dist matches a fresh build of ui/src'
