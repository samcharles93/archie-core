#!/usr/bin/env bash
# Cuts a release in two steps, so the generated notes can be edited before
# they are frozen into a tag -- the same review window release-please gives
# you with its release PR.
#
#   tools/release.sh --version 1.2.0 --dry-run   # preview
#   tools/release.sh --version 1.2.0 --prepare   # write, then edit
#   tools/release.sh --version 1.2.0 --tag       # commit + tag
#
# There is one release stream: CHANGELOG.md carries every release section,
# one generated page per version, and one tag names the release. A release is
# one version covering whatever changed; per-component detail is a labelled
# section inside it, not a separate number.
#
# Exit codes: 0 released (or previewed/prepared); 1 failure; 3 nothing to
# release -- a commit set that justifies no version is a result, not an error,
# so a pipeline can finish green without tagging.
set -euo pipefail

cd "$(dirname "$0")/.."

NOTHING_TO_RELEASE=3
MODE=""
VERSION=""

die() {
	echo "release: $*" >&2
	exit 1
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version) VERSION="${2:-}"; shift 2 ;;
	--dry-run) MODE="dry-run"; shift ;;
	--prepare) MODE="prepare"; shift ;;
	--tag) MODE="tag"; shift ;;
	*) die "unknown argument: $1" ;;
	esac
done

[ -n "$VERSION" ] || die "--version <version> is required"
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

# The host bundle IS the archied artifact: the distribution zip is named and
# versioned by the release, and every process it ships carries that version.
# The paths below are named rather than whole directories, so a packaging-only
# change is attributed to archied deliberately and not every CI or script edit.
archied_dirs() {
	component_dirs ./cmd/archied
	# Every process the host bundles ships in this release, so a change to a
	# package only one of them links still lands in the changelog. Without
	# these, messaging-only, state-store-only and playbooks-only packages closed
	# over no component and the release silently omitted them (the telegram fence
	# fix was dropped this way). The gap is pre-existing -- the old two-file
	# closures named only cmd/archied and cmd/archie-agent -- but it grew teeth
	# when the Messaging Service was severed from archied in 1.35.0.
	component_dirs ./cmd/archie-messaging
	component_dirs ./cmd/archie-state-store
	component_dirs ./cmd/archie-playbooks
	component_dirs ./cmd/archie-ui
	printf '%s\n' "cmd/archied" "Dockerfile.archied" "cmd/archie-ui" "internal/app/archieui" \
		"Taskfile.yml" ".github/workflows/deploy.yml" "install.sh" \
		"scripts/archie-update-install" "scripts/archie-update-check" \
		"scripts/archie-update-watchdog" "deployments/INSTRUCTIONS.md"
}

# The UI Service ships with the archied release on purpose (archie-ui shares
# internal/webui with archied), hence archied_dirs' extra paths above.
runtime_dirs() {
	component_dirs ./cmd/archie-agent
	printf '%s\n' "cmd/archie-agent" "Dockerfile"
}

# changelog_body -- labelled per-component sections for every releasable commit
# since the last release. Empty output means nothing to release: a commit set
# that justifies no version produces no body, and the caller reports that
# rather than inventing one.
changelog_body() {
	local last range archied runtime
	last="$(git describe --tags --abbrev=0 2>/dev/null || true)"
	range="HEAD"
	if [ -n "$last" ]; then
		range="$last..HEAD"
	fi
	archied="$(archied_dirs)"
	runtime="$(runtime_dirs)"

	local sha subject files in_archied="" in_runtime=""
	while read -r sha; do
		[ -n "$sha" ] || continue
		subject="$(git log -1 --pretty=%s "$sha")"
		# Conventional commits only; release commits are not news.
		case "$subject" in
		feat:* | feat\(*|fix:* | fix\(*|perf:* | perf\(*|refactor:* | refactor\(*) ;;
		*) continue ;;
		esac
		files="$(git show --pretty=format: --name-only "$sha")"
		if matches "$files" "$archied"; then
			in_archied="$in_archied- $subject"$'\n'
		fi
		if matches "$files" "$runtime"; then
			in_runtime="$in_runtime- $subject"$'\n'
		fi
	done < <(git log --no-merges --pretty=%H --reverse "$range")

	if [ -n "$in_archied" ]; then
		printf '### archied\n\n%s\n' "$in_archied"
	fi
	if [ -n "$in_runtime" ]; then
		printf '### archie-agent\n\n%s\n' "$in_runtime"
	fi
}

# prepend <version> <body-file> -- insert the new section after the title and
# any [Unreleased] block, which stays at the top, and above every release.
prepend() {
	local version="$1" bodyfile="$2" tmp line
	line="$(grep -n '^## \[' CHANGELOG.md | grep -v '## \[Unreleased\]' | head -1 | cut -d: -f1)"
	if [ -z "$line" ]; then
		line="$(($(wc -l <CHANGELOG.md) + 1))"
	fi
	tmp="$(mktemp)"
	{
		head -n "$((line - 1))" CHANGELOG.md
		echo "## [$version] - $(date +%F)"
		echo
		cat "$bodyfile"
		echo
		tail -n "+$line" CHANGELOG.md
	} >"$tmp"
	mv "$tmp" CHANGELOG.md
}

body="$(changelog_body)"

if [ -z "$body" ]; then
	last="$(git describe --tags --abbrev=0 2>/dev/null || echo 'the beginning')"
	echo "nothing to release: no releasable commits since $last" >&2
	exit "$NOTHING_TO_RELEASE"
fi

echo "==> release $VERSION"
echo "$body" | sed 's/^/    /'

if [ "$MODE" = "dry-run" ]; then
	echo
	echo "dry run: no files written, no commits, no tags"
	exit 0
fi

bodyfile="$(mktemp)"
printf '%s\n' "$body" >"$bodyfile"

if [ "$MODE" = "prepare" ]; then
	prepend "$VERSION" "$bodyfile"
	rm -f "$bodyfile"
	echo
	echo "CHANGELOG.md updated and left uncommitted. Edit it -- generated notes"
	echo "are a starting point, not the release -- then freeze it with:"
	echo
	echo "    tools/release.sh --version $VERSION --tag"
	exit 0
fi

# --tag: the changelog must already describe this release.
grep -q "^## \\[$VERSION\\]" CHANGELOG.md ||
	die "CHANGELOG.md has no [$VERSION] section; run --prepare first"

# The published release notes are derived from the changelog, so regenerate
# them after the maintainer's edit and ship them in the same commit as the
# section they describe. newsgen skips a file whose bytes already match, so a
# re-run leaves the tree clean.
go -C tools run -mod=readonly ./newsgen --repo-root ..

git add CHANGELOG.md docs/news
git commit -m "chore(release): v$VERSION"
git tag -a "v$VERSION" -m "v$VERSION"

echo
echo "tagged. push with:"
echo "    git push origin main --follow-tags"
echo
echo "The deploy workflow reads the tags pointing at HEAD, so pushing the"
echo "commit without its tag builds images stamped 'dev'."
