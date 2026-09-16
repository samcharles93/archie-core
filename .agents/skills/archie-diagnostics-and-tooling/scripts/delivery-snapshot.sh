#!/bin/sh
set -eu

if [ "$#" -ne 0 ]; then
	printf '%s\n' "usage: delivery-snapshot.sh" >&2
	exit 2
fi

for tool in git rg sed wc; do
	command -v "$tool" >/dev/null 2>&1 || {
		printf 'delivery-snapshot: %s is required\n' "$tool" >&2
		exit 127
	}
done

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd "$script_dir/../../../.." && pwd)
if [ ! -f "$repo_root/go.mod" ] || [ ! -f "$repo_root/Taskfile.yml" ]; then
	printf 'delivery-snapshot: expected Archie repository root at %s\n' "$repo_root" >&2
	exit 2
fi

temporary=$(mktemp -d "${TMPDIR:-/tmp}/archie-delivery-snapshot.XXXXXX")
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
cd "$repo_root"

printf '%s\n' "META	schema	archie-delivery-snapshot/v1"

scan_literal() {
	axis=$1
	scope=$2
	pattern=$3
	shift 3
	result="$temporary/$axis"
	if rg -n --no-heading --color never -e "$pattern" "$@" >"$result"; then
		:
	else
		status=$?
		if [ "$status" -ne 1 ]; then
			printf 'delivery-snapshot: scan failed for %s\n' "$axis" >&2
			exit "$status"
		fi
	fi
	count=$(wc -l <"$result" | sed 's/[[:space:]]//g')
	printf 'SURFACE\t%s\t%s\t%s\n' "$axis" "$scope" "$count"
}

scan_literal task_format taskfile 'gofumpt[[:space:]]+-w|go fix' Taskfile.yml
scan_literal task_vet taskfile 'go vet' Taskfile.yml
scan_literal task_build taskfile 'go build|task:[[:space:]]+build' Taskfile.yml
scan_literal task_test taskfile 'go test|task:[[:space:]]+test' Taskfile.yml
scan_literal task_lint taskfile 'golangci-lint|task:[[:space:]]+lint' Taskfile.yml
scan_literal task_race taskfile 'go test[^#\n]*-race|-race[^#\n]*go test' Taskfile.yml
scan_literal task_tools_module taskfile 'go -C tools|cd tools|task:[[:space:]]+tools' Taskfile.yml
scan_literal task_docs taskfile 'docsgen|docs:generate|docs:check' Taskfile.yml

# One CI surface: .github/workflows. The Gitea workflow was dropped in e539048
# ("ci: build and push images to GHCR, drop the Gitea workflow"), so scanning
# .gitea/workflows aborted the whole snapshot under `set -eu`.
scan_literal github_go_gate github-workflows 'go test|go vet|go build|golangci-lint|task[[:space:]]+check' .github/workflows
scan_literal github_container_publish github-workflows 'docker build|docker push|docker/build-push-action|docker login' .github/workflows

# Composition root. cmd/archied/main.go is now a ~30-line dispatcher that only
# routes to internal/app/archied, so anchoring on it reported a silent zero for
# every row below -- which reads as "no composition root" rather than "wrong
# file". The wiring lives in internal/app/archied.
scan_literal production_config_resolve composition-root 'configuration\.New\(' internal/app/archied
scan_literal production_config_overlay composition-root 'ApplyOverlayValues\(|loadConfig\(' internal/app/archied
scan_literal production_sqlite_store composition-root 'openProductionTaskStore\(|openStateStore\(' internal/app/archied
scan_literal production_daemon composition-root '&daemon\.Daemon\{' internal/app/archied
scan_literal production_rpc_servers composition-root 'registerTaskRPCServers\(' internal/app/archied

git ls-files >"$temporary/tracked"
tracked_total=$(wc -l <"$temporary/tracked" | sed 's/[[:space:]]//g')
tracked_node_modules=$(rg -c '(^|/)node_modules(/|$)' "$temporary/tracked" || true)
tracked_artifacts=$(rg -c '(^|/)(node_modules|bin|dist|\.gotmp)(/|$)|\.(exe|test|out)$' "$temporary/tracked" || true)
tracked_symlinks=$(git ls-files -s | awk '$1 == "120000" {count++} END {print count + 0}')

printf 'HYGIENE\ttracked_paths\t%s\n' "$tracked_total"
printf 'HYGIENE\ttracked_node_modules_paths\t%s\n' "${tracked_node_modules:-0}"
printf 'HYGIENE\ttracked_artifact_candidates\t%s\n' "${tracked_artifacts:-0}"
printf 'HYGIENE\ttracked_symlinks\t%s\n' "$tracked_symlinks"
if [ -f .dockerignore ]; then
	printf '%s\n' "HYGIENE	dockerignore_present	1"
else
	printf '%s\n' "HYGIENE	dockerignore_present	0"
fi

find . \
	\( -path './.git' -o -path './.claude' -o -path './.references' -o -name node_modules -o -name .worktrees \) -prune -o \
	-name go.mod -print |
	LC_ALL=C sort |
	while IFS= read -r module; do
		printf 'MODULE\t%s\n' "$module"
	done
