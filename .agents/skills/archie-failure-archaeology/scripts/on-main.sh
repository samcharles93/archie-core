#!/bin/sh
# on-main.sh -- did this ref land on the trunk, even when it was squash- or
# rebase-merged?
#
# `git merge-base --is-ancestor` answers a topology question: is this exact
# commit object an ancestor. GitHub's "Squash and merge" always mints a new
# commit, and "Rebase and merge" mints new commits whenever the branch had to
# be replayed, so both orphan the SHA your branch holds. The raw check then
# reports a landed repair as never-landed, and `git branch -a --contains` is
# blind in exactly the same way.
#
# This wrapper keeps `--is-ancestor` as the offline fast path and falls back to
# the GitHub API, which records merge state independently of the commit graph.
#
# usage: on-main.sh [-q] <ref> [base-ref]
#
#   <ref>       commit, tag, or branch to test
#   [base-ref]  trunk to test against (default: origin/main)
#   -q          print nothing; report through the exit code only
#
# exit 0   landed on <base-ref>, by ancestry or by a merged pull request
# exit 1   not landed: known to GitHub, but in no merged pull request
# exit 2   undetermined: bad ref, no gh, offline, or never pushed to GitHub
# exit 127 git is missing
#
# exit 2 is not a negative. Branch on the code explicitly; `if on-main.sh <ref>`
# folds it into "not landed", which is the error this helper exists to prevent.

set -eu

quiet=0
while [ "$#" -gt 0 ]; do
	case "$1" in
		-q | --quiet)
			quiet=1
			shift
			;;
		--)
			shift
			break
			;;
		-*)
			printf 'on-main: unknown option %s\n' "$1" >&2
			printf '%s\n' 'usage: on-main.sh [-q] <ref> [base-ref]' >&2
			exit 2
			;;
		*)
			break
			;;
	esac
done

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
	printf '%s\n' 'usage: on-main.sh [-q] <ref> [base-ref]' >&2
	exit 2
fi

ref=$1
base=${2:-origin/main}

say() {
	[ "$quiet" -eq 1 ] || printf '%s\n' "$1"
}

command -v git >/dev/null 2>&1 || {
	printf '%s\n' 'on-main: git is required' >&2
	exit 127
}

sha=$(git rev-parse --verify --quiet "${ref}^{commit}") || {
	say "undetermined: $ref does not resolve to a commit in this repository"
	exit 2
}

git rev-parse --verify --quiet "${base}^{commit}" >/dev/null 2>&1 || {
	say "undetermined: base ref $base does not resolve; fetch it, or name the trunk explicitly"
	exit 2
}

if git merge-base --is-ancestor "$sha" "$base" 2>/dev/null; then
	say "landed: $sha is an ancestor of $base (topology)"
	exit 0
fi

# Not an ancestor. The SHA may still have landed if GitHub rewrote it at merge
# time, so ask the service that recorded the merge rather than the commit graph.
command -v gh >/dev/null 2>&1 || {
	say "undetermined: $sha is not an ancestor of $base, and gh is absent, so a squash or rebase merge cannot be ruled out"
	exit 2
}

merged=$(
	gh api "repos/{owner}/{repo}/commits/$sha/pulls" \
		--jq '[.[] | select(.merged_at != null)] | length' 2>/dev/null
) || {
	say "undetermined: $sha is not an ancestor of $base, and GitHub could not answer for it (offline, unauthenticated, or never pushed)"
	exit 2
}

case "$merged" in
	'' | *[!0-9]*)
		say "undetermined: GitHub returned an unreadable answer for $sha"
		exit 2
		;;
	0)
		say "not-landed: $sha is not an ancestor of $base and belongs to no merged pull request"
		exit 1
		;;
	*)
		say "landed: $sha is not an ancestor of $base, but GitHub records it in $merged merged pull request(s) -- a squash or rebase merge rewrote the SHA"
		exit 0
		;;
esac
