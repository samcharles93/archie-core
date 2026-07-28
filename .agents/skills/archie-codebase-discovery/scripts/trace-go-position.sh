#!/bin/sh
set -u

usage() {
	printf '%s\n' "usage: $0 file.go:line:column" >&2
	exit 2
}

[ "$#" -eq 1 ] || usage
position=$1

command -v go >/dev/null 2>&1 || {
	printf '%s\n' "error: go is required" >&2
	exit 127
}
command -v gopls >/dev/null 2>&1 || {
	printf '%s\n' "error: gopls is required" >&2
	exit 127
}

case "$position" in
	*.go:[1-9]*:[1-9]*) ;;
	*) usage ;;
esac

cache_root=${ARCHIE_DISCOVERY_CACHE:-"${TMPDIR:-/tmp}/archie-codebase-discovery"}
mkdir -p "$cache_root/go-build" "$cache_root/xdg" "$cache_root/tmp" || exit 1

export GOCACHE="$cache_root/go-build"
export XDG_CACHE_HOME="$cache_root/xdg"
export GOTMPDIR="$cache_root/tmp"
export GOPROXY=off
export GOFLAGS=-mod=readonly

gomod=$(go env GOMOD)
if [ "$gomod" = "/dev/null" ] || [ -z "$gomod" ]; then
	printf '%s\n' "error: run from inside a Go module" >&2
	exit 1
fi

run_query() {
	label=$1
	shift
	printf '\n== %s ==\n' "$label"
	if output=$(gopls "$@" 2>&1); then
		printf '%s\n' "$output"
		return 0
	else
		printf '%s\n' "$output"
		printf '%s\n' "(query is not applicable at this position, or failed)"
		return 1
	fi
}

successful=0
run_query "definition" definition "$position" && successful=$((successful + 1))
run_query "references (including declaration)" references -d "$position" && successful=$((successful + 1))
run_query "implementations" implementation "$position" && successful=$((successful + 1))
run_query "call hierarchy" call_hierarchy "$position" && successful=$((successful + 1))

[ "$successful" -gt 0 ]
