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

### The merged history

Two version streams become one file, so a version that both components released
carries one heading and one date: **the newest date among that version's sections**,
because a version's date is when the last part of it shipped. Where a component's own
date differs from it, that component's section label carries its own date, so the
per-component fact survives the merge rather than being collapsed into the heading.

This ambiguity is historical only. One version stream with one tag gives a version
exactly one date, so the rule represents the merged past rather than a design that has
to hold.

The merge is verified by content, not by the generator succeeding: the section count
per source, the merged version set equal to the union of both sources with nothing
extra, and every section title still present. A generator that agrees with the file it
just wrote is evidence of nothing.

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

## The release pipeline

**A Woodpecker workflow performs the release**: gate, generate the release page, run
the reviewer over it, commit the changelog and the generated pages, tag, push,
publish images, and trigger the site's artifact sync. No step in that list is an
operator's.

**It triggers on a push to main**, not on a manual run. A pipeline a human must start
is a pipeline that can be forgotten, which is the property this change removes. The
trigger is also not dependent on repairing manual triggering: in this estate a manual
run fails with zero steps while the same configuration runs on a push, and the
estate's own Woodpecker configurations trigger on push with cron, manual or
pull-request events *added* to it rather than instead of it.

**Prerequisite: Woodpecker must watch this repository.** Adding the workflow to the
repository and enabling the repository in Woodpecker are two steps, and the second is
a configuration action rather than a file. A release pipeline that is correct and
never runs is invisible until someone asks why no release happened, so the repository
being enabled is a prerequisite of this design rather than an assumption behind it.

**One tag, and the duplicate-build problem shrinks as a consequence.** Concurrency
keyed on a commit identifier treats an annotated tag as a ref distinct from its
commit, which starts three builds for one release. One version and one tag removes
two of those refs. Nothing in the pipeline may depend on a tag object's
identity, and this is a consequence of one version stream rather than a separate fix.

### The version is computed, not typed

The version is derived from the conventional commits since the last release, which is
what the commit rules above exist for.

- `feat` bumps the minor version; `fix`, `perf` and `refactor` bump the patch.
- **A breaking change bumps the major version.** Its form is `!` after the type or
  scope, as in `feat(api)!:`, and a `BREAKING CHANGE:` trailer — either marks it.
- `chore`, `docs`, `test`, `style` and `build` bump nothing alone.
- **No release is a legitimate outcome.** A commit set that justifies no version
  produces no version, and the pipeline reports that rather than inventing one.

### `tools/release.sh` is designed for the pipeline

- **It never prompts.** Every input arrives as an argument or in the environment, so
  it runs with no terminal.
- **Its version is a result as well as an argument.** It may be given one or asked to
  derive one, and it reports the version it used in a form a pipeline can read.
- **"Nothing to release" has its own exit code**, distinct from failure, so the
  pipeline finishes green without tagging.
- **It commits and tags nothing.** The pipeline owns the commit, the tag and the push;
  the script writes the notes and reports what it did. A script that pushes cannot be
  run twice safely, and this one runs on every push to main.

### Which CI is authoritative

Three surfaces exist: GitHub Actions, the Gitea workflow files, and Woodpecker.

- **Woodpecker owns releases**, which is the production path this repository's deploy
  workflow already names.
- **GitHub Actions keeps the checks a pull request needs.**
- **The Gitea workflow files are retired in favour of Woodpecker** once Woodpecker
  runs the same checks, and are not edited in the meantime: two copies of a pipeline
  diverge.
- No check lives in two places with neither authoritative. Where a check must run in
  both, the second runs the same file rather than a copy of it.

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
- A push to main carrying releasable commits produces a version, a release page, a
  tag and a published image with no operator step.
- A push whose commits justify no release ends green and tags nothing.
- `tools/release.sh` runs to completion with stdin closed, and exits with its
  distinct code when there is nothing to release.
- The release workflow reads no tag object's identity, so one release starts one
  build.
