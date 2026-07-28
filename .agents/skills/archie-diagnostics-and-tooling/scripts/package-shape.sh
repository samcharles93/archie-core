#!/bin/sh
set -eu

if [ "$#" -gt 1 ]; then
	printf '%s\n' "usage: $0 [module-directory-relative-to-repo]" >&2
	exit 2
fi

command -v go >/dev/null 2>&1 || {
	printf '%s\n' "package-shape: go is required" >&2
	exit 127
}
command -v awk >/dev/null 2>&1 || {
	printf '%s\n' "package-shape: awk is required" >&2
	exit 127
}

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd "$script_dir/../../../.." && pwd)
module_relative=${1:-.}
module_root="$repo_root/$module_relative"
edge_helper="$repo_root/.claude/skills/archie-codebase-discovery/scripts/package-edges.sh"

if [ ! -f "$module_root/go.mod" ]; then
	printf 'package-shape: no go.mod at %s\n' "$module_root" >&2
	exit 2
fi
if [ ! -x "$edge_helper" ]; then
	printf 'package-shape: required sibling helper is missing or not executable: %s\n' "$edge_helper" >&2
	exit 2
fi

temporary=$(mktemp -d "${TMPDIR:-/tmp}/archie-package-shape.XXXXXX")
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
mkdir -p "$temporary/cache" "$temporary/build-tmp"

export GOCACHE="$temporary/cache"
export GOTMPDIR="$temporary/build-tmp"
export GOPROXY=off
export GOFLAGS="-mod=readonly -buildvcs=false"
export ARCHIE_DISCOVERY_CACHE="$temporary/discovery"

(
	cd "$module_root"
	go list ./... | LC_ALL=C sort
) >"$temporary/packages"
if (
	cd "$module_root"
	"$edge_helper" ./...
) >"$temporary/edges" 2>"$temporary/edge-stderr"; then
	# The sibling helper sets its own GOFLAGS and can trigger a harmless,
	# nondeterministic temporary filename when GOMODCACHE is read-only.
	# Preserve the warning while normalizing only that filename suffix.
	sed 's/\.info[0-9][0-9]*\.tmp/.info<TEMP>.tmp/' "$temporary/edge-stderr" >&2
else
	status=$?
	cat "$temporary/edge-stderr" >&2
	exit "$status"
fi
(
	cd "$module_root"
	go list -m -f '{{.Path}}'
) >"$temporary/module"

module_path=$(sed -n '1p' "$temporary/module")
printf '%s\n' "META	schema	archie-package-shape/v1"
printf 'META\tmodule\t%s\n' "$module_path"

awk -F '	' '
	NR == FNR {
		packages[$0] = 1
		next
	}
	{
		split($0, edge, " -> ")
		outbound[edge[1]]++
		inbound[edge[2]]++
	}
	END {
		for (packageName in packages) {
			printf "PACKAGE\t%s\t%d\t%d\n", packageName, inbound[packageName] + 0, outbound[packageName] + 0
		}
	}
' "$temporary/packages" "$temporary/edges" | LC_ALL=C sort

awk '{printf "EDGE\t%s\n", $0}' "$temporary/edges"
