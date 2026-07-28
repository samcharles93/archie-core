#!/bin/sh
set -eu

if [ "$#" -gt 1 ]; then
	printf '%s\n' "usage: $0 [go-package-pattern]" >&2
	exit 2
fi

command -v go >/dev/null 2>&1 || {
	printf '%s\n' "error: go is required" >&2
	exit 127
}

pattern=${1:-./...}
cache_root=${ARCHIE_DISCOVERY_CACHE:-"${TMPDIR:-/tmp}/archie-codebase-discovery"}
mkdir -p "$cache_root/go-build" "$cache_root/tmp"

export GOCACHE="$cache_root/go-build"
export GOTMPDIR="$cache_root/tmp"
export GOPROXY=off
export GOFLAGS=-mod=readonly

module_path=$(go list -m -f '{{.Path}}')

go list -f '{{.ImportPath}}{{range .Imports}}{{printf "\t%s" .}}{{end}}' "$pattern" |
	awk -F '	' -v module="$module_path" '
		{
			from = $1
			for (i = 2; i <= NF; i++) {
				if ($i == module || index($i, module "/") == 1) {
					printf "%s -> %s\n", from, $i
				}
			}
		}
	' |
	LC_ALL=C sort -u
