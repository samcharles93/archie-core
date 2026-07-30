#!/usr/bin/env bash
# Cuts a release in two steps, so the generated notes can be edited before
# they are frozen into a tag -- the same review window release-please gives
# you with its release PR.
#
#   tools/release.sh --gateway 1.2.0 --runtime 1.2.0 --dry-run   # preview
#   tools/release.sh --gateway 1.2.0 --runtime 1.2.0 --prepare   # write, then edit
#   tools/release.sh --gateway 1.2.0 --runtime 1.2.0 --tag       # commit + tag
#
# archied and archie-agent are independently versioned (see CHANGELOG.md),
# so each gets its own version, tag and changelog. Pass "skip" for a
# component that is not moving this release.
#
# Which commits land in which changelog is decided by the binary's actual
# package closure (go list -deps), not by guessing from the commit subject:
# a change to a package archied does not import cannot appear in archied's
# changelog. Commits touching neither closure (docs, CI, deployment) are
# left out of both.
set -euo pipefail

cd "$(dirname "$0")/.."

MODE=""
GATEWAY_VERSION=""
RUNTIME_VERSION=""

die() {
	echo "release: $*" >&2
	exit 1
}

while [ $# -gt 0 ]; do
	case "$1" in
	--gateway) GATEWAY_VERSION="${2:-}"; shift 2 ;;
	--runtime) RUNTIME_VERSION="${2:-}"; shift 2 ;;
	--dry-run) MODE="dry-run"; shift ;;
	--prepare) MODE="prepare"; shift ;;
	--tag) MODE="tag"; shift ;;
	*) die "unknown argument: $1" ;;
	esac
done

[ -n "$GATEWAY_VERSION" ] || die "--gateway <version|skip> is required"
[ -n "$RUNTIME_VERSION" ] || die "--runtime <version|skip> is required"
[ -n "$MODE" ] || die "one of --dry-run, --prepare or --tag is required"

if [ "$MODE" != "dry-run" ]; then
	branch="$(git rev-parse --abbrev-ref HEAD)"
	[ "$branch" = "main" ] || die "releases are cut from main, not $branch"
fi
if [ "$MODE" = "prepare" ]; then
	[ -z "$(git status --porcelain)" ] || die "working tree is dirty; commit or stash first"
fi

# component_dirs <cmd-path> -- repo-relative dirs of every package the binary
# links, plus the files that build only that binary.
component_dirs() {
	go list -deps -f '{{.Dir}}' "$1" 2>/dev/null |
		grep -F "$PWD/" |
		sed "s|^$PWD/||"
}

# section <tag-prefix> <cmd-path> <extra-path>... -- changelog body for the
# commits since <tag-prefix>'s last tag that touched this component.
section() {
	local prefix="$1" cmd="$2"
	shift 2
	local last range dirs
	last="$(git tag --list "$prefix/v*" | sort -V | tail -1)"
	range="HEAD"
	if [ -n "$last" ]; then
		range="$last..HEAD"
	fi

	dirs="$(component_dirs "$cmd")"$'\n'"$(printf '%s\n' "$@")"

	local sha subject files
	while read -r sha; do
		[ -n "$sha" ] || continue
		subject="$(git log -1 --pretty=%s "$sha")"
		# Conventional commits only; release commits are not news.
		case "$subject" in
		feat:* | feat\(*|fix:* | fix\(*|perf:* | perf\(*|refactor:* | refactor\(*) ;;
		*) continue ;;
		esac
		files="$(git show --pretty=format: --name-only "$sha")"
		if matches "$files" "$dirs"; then
			echo "- $subject"
		fi
	done < <(git log --no-merges --pretty=%H --reverse "$range")
}

# matches <changed-files> <dirs> -- true when any changed file lives in one
# of the component's directories.
matches() {
	local files="$1" dirs="$2" f d
	while read -r f; do
		[ -n "$f" ] || continue
		while read -r d; do
			[ -n "$d" ] || continue
			case "$f" in
			"$d"/*) return 0 ;;
			"$d") return 0 ;;
			esac
		done <<<"$dirs"
	done <<<"$files"
	return 1
}

# prepend <changelog> <version> <body> -- insert a dated section directly
# under the file's title.
prepend() {
	local file="$1" version="$2" body="$3" tmp
	tmp="$(mktemp)"
	{
		head -1 "$file"
		echo
		echo "## [$version] - $(date +%F)"
		echo
		echo "$body"
		tail -n +2 "$file"
	} >"$tmp"
	# Collapse the blank-line run left where the old body started.
	awk 'BEGIN{blank=0} /^$/{blank++; if(blank>1) next} !/^$/{blank=0} {print}' "$tmp" >"$file"
	rm -f "$tmp"
}

release_component() {
	local name="$1" version="$2" prefix="$3" changelog="$4" cmd="$5"
	shift 5
	if [ "$version" = "skip" ]; then
		echo "==> $name: skipped"
		return
	fi
	local body
	body="$(section "$prefix" "$cmd" "$@")"
	if [ -z "$body" ]; then
		body="- chore: no user-facing changes"
	fi
	echo "==> $name $version ($prefix/v$version)"
	echo "$body" | sed 's/^/    /'
	if [ "$MODE" = "prepare" ]; then
		prepend "$changelog" "$version" "$body"
	fi
}

release_component "archied" "$GATEWAY_VERSION" "archied" \
	"CHANGELOG.archied.md" "./cmd/archied" "cmd/archied" "Dockerfile.archied"

release_component "archie-agent" "$RUNTIME_VERSION" "archie" \
	"CHANGELOG.archie.md" "./cmd/archie-agent" "cmd/archie-agent" "Dockerfile"

if [ "$MODE" = "dry-run" ]; then
	echo
	echo "dry run: no files written, no commits, no tags"
	exit 0
fi

if [ "$MODE" = "prepare" ]; then
	echo
	echo "changelogs updated and left uncommitted. Edit them -- generated notes"
	echo "are a starting point, not the release -- then freeze them with:"
	echo
	echo "    tools/release.sh --gateway $GATEWAY_VERSION --runtime $RUNTIME_VERSION --tag"
	exit 0
fi

# --tag: the changelog must already describe this release.
[ "$GATEWAY_VERSION" = "skip" ] || grep -q "^## \\[$GATEWAY_VERSION\\]" CHANGELOG.archied.md ||
	die "CHANGELOG.archied.md has no [$GATEWAY_VERSION] section; run --prepare first"
[ "$RUNTIME_VERSION" = "skip" ] || grep -q "^## \\[$RUNTIME_VERSION\\]" CHANGELOG.archie.md ||
	die "CHANGELOG.archie.md has no [$RUNTIME_VERSION] section; run --prepare first"

# Plain `[ cond ] && cmd` would abort the script under set -e whenever cond
# is false, i.e. whenever a component is skipped.
msg="chore(release):"
if [ "$GATEWAY_VERSION" != "skip" ]; then
	msg="$msg archied v$GATEWAY_VERSION"
fi
if [ "$RUNTIME_VERSION" != "skip" ]; then
	msg="$msg archie v$RUNTIME_VERSION"
fi

git add CHANGELOG.archied.md CHANGELOG.archie.md
git commit -m "$msg"

if [ "$GATEWAY_VERSION" != "skip" ]; then
	git tag -a "archied/v$GATEWAY_VERSION" -m "archied v$GATEWAY_VERSION"
fi
if [ "$RUNTIME_VERSION" != "skip" ]; then
	git tag -a "archie/v$RUNTIME_VERSION" -m "archie-agent v$RUNTIME_VERSION"
fi

echo
echo "tagged. push with:"
echo "    git push origin main --follow-tags"
echo
echo "The deploy workflow reads the tags pointing at HEAD, so pushing the"
echo "commit without its tags builds images stamped 'dev'."
