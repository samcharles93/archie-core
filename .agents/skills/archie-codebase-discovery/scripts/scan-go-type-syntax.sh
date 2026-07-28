#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cache_root=${ARCHIE_DISCOVERY_CACHE:-"${TMPDIR:-/tmp}/archie-codebase-discovery"}
mkdir -p "$cache_root/go-build" "$cache_root/tmp"

export GOCACHE="$cache_root/go-build"
export GOTMPDIR="$cache_root/tmp"
export GOPROXY=off
export GOFLAGS=-mod=readonly

exec go run "$script_dir/scan-go-type-syntax.go" "$@"
