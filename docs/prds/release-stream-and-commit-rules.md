# One release stream, one page per version, and commit rules — decision

**Status:** Draft
**Date:** 2026-09-22
**Authority:** `RELEASING.md` for the process this changes, `AGENTS.md` for the
commit rules it enforces, and `tools/newsgen` for the artifact shape.

**Supersedes:** `docs/prds/news-and-releases.md`, sections "Source of truth" and
"Two components" — one changelog replaces two, and one page per version replaces
one page per component. Its remaining decisions stand.

## Decision

**A release is one version covering whatever changed.** One changelog holds every
release section, one generated page holds every version's notes, and an operator
cutting a release names one version.

## One changelog

Every release section lives in `CHANGELOG.md`, newest first. `CHANGELOG.archied.md`
and `CHANGELOG.archie.md` are deleted, and `CHANGELOG.md` stops being a pointer to
them. The changelog is the only source of release prose, and tags are not a source:
CI and deployment check out shallow, so tags are absent from a site build while the
changelog is always present.

## One version stream

**`GATEWAY` and `RUNTIME` stop being release inputs.** `release:prepare` and
`release:tag` take one version, and `tools/release.sh` loses the two flags.

They may stay as build-time stamps, because a binary must still report what it
contains. A release that must name three versions forces an operator to pass skip
values for components that did not change, and that ceremony is what this decision
removes — along with the drift it allows, where two components' tags fall a release
apart.

**Per-component detail stays, as sections inside the release.** A reader still needs
to know whether `archied` or the agent changed; what goes away is the separate
numbers and the separate pages, not the information.

**History is not re-tagged.** Existing tags keep their names, because tags are not a
source and nothing reads them to build a page. A future reader must not "fix" the
unified changelog by rewriting tags to match it.

## One release page per version

`docs/news/<version>.md` replaces `docs/news/<component>/<version>.md`, carrying the
release's per-component sections inside it.

**Every release URL already published keeps working.** `/news/archied/<version>` and
`/news/archie/<version>` route to the unified page for that version. The generator
emits the redirect table beside the pages so it cannot drift from them; if the
renderer needs the table in its own repository, that is a change in that repository
rather than a hand-maintained list here.

## The reviewer runs between prepare and tag

Release notes are prose, and the review agent exists to review prose. **`release:tag`
refuses to tag when the review has not been run for that version, or when it reports
at the level.**

The invocation is verified rather than assumed. `tl rules --lint` reports `corpus ok`
and exits 0. `tl review <path> --deadline <ms> --max-turns <n>` reports
`0 findings (0 high, 0 medium) · 1/1 files reviewed · clean` and exits 0 on
`docs/news/archied/1.36.0.md`.

**`--fail-on` must be passed explicitly.** It defaults to `never`, so an invocation
without it cannot refuse anything and the step would be decorative. `--fail-on high`
is the level: a medium finding in a release note is worth acting on, and it should not
stop a release on its own.

**The rule is stated once.** It belongs in the reviewer's own rule set, which
`tl rules --from-doc` can draft from this document; `RELEASING.md` points at that
rule beside the step it governs rather than restating it, because two statements of
one rule drift.

## Commit rules and their enforcement

- **Conventional commits**, with the scope taken from the package map.
- **One logical change per commit.** A commit that does two things is two commits.
- **The subject is imperative and short**; the body says why the change is right
  rather than restating the diff.
- **A commit depends on the gate's exit status.** The gate and the commit run as one
  conditional chain, never as separate statements on one command line, where nothing
  reads the gate's status but the operator who typed it.
- **Enforcement is a `commit-msg` hook** installed by one documented target, plus a
  check in CI over the pushed range, so the rule holds for a person who never
  installed the hook.
- **Scopes are derived, not invented**, and an unknown scope fails loudly rather than
  being accepted.

## Verification

- One changelog file exists, the two component changelogs do not, and the generated
  pages match it.
- A release prepared with `VERSION` alone writes its notes and tags, with no
  `GATEWAY` or `RUNTIME` input.
- Every release URL published before this change resolves.
- `release:tag` refuses after a review that reported at the level, and refuses when
  no review ran.
- The `commit-msg` hook and the CI range check each refuse a non-conforming subject
  and an unknown scope, with the hook absent and present.
