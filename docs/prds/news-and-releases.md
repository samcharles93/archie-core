# Public news and per-version release notes

**Status:** Approved
**Date:** 2026-09-21
**Beads issue:** `archie-core-2ncm.2`
**Superseded in part:** `docs/prds/release-stream-and-commit-rules.md` defines the
single release stream. `CHANGELOG.md` carries every release and one `vX.Y.Z` tag
names each release.

The Astro website renders public news and per-version release notes from the
artifacts generated here. Both derive from `CHANGELOG.md`, so publishing a
release adds no writing.

## Source of truth

Release notes come from the frozen sections in `CHANGELOG.md`:
`tools/release.sh --prepare` writes a section, and `--tag` refuses to tag a
version without it. The generated artifact has one entry per release version.

Tags and raw commits are not the source. The changelog is the edited release
record, and its `## [<version>] - <date>` headings provide the version and date.

## Release data

The generator skips `## [Unreleased]`. Any other `## [` heading that is not
`[version] - date` stops generation. A relative Markdown link in a changelog
body also stops generation because the Astro site must be able to render the
content without depending on repository-relative paths.

`archied` and `archie-agent` version independently. Each
`docs/news/releases.json` entry carries the tag prefix (`archied`, `archie`),
product name (`archied`, `archie-agent`), version, date, and changelog body.
`docs/news/redirects.json` maps retired per-component news URLs to each unified
release route. The Astro site must apply this table so previously published
release URLs continue to resolve.

Both JSON files are committed generated artifacts. `task news` regenerates
them, `task news:check` detects drift, and `tools/release.sh --tag` regenerates
and stages them with the release commit. The Astro site renders from these
artifacts rather than re-parsing `CHANGELOG.md`.

## Site ownership

The Astro website is maintained outside this repository and serves the
documentation at `offloaded.dev/docs/`. This repository generates and validates
the news and documentation data artifacts; it does not build, preview, or
deploy the Astro site.

## Not in scope

- Generated summaries, categories, or section headings; the changelog has no
  structured summary field to derive one from.
- Per-release download or install links; where to get the artifacts is
  documented once, not restated for every release.
- Hand-written news items.
- Changes to `infra/ansible` or to the external Astro website repository.

## Verification

- `go test ./tools/newsgen/...` covers parsing, ordering, artifact generation,
  heading and link refusals, and check-mode drift.
- `task news:check` passes inside `task check`.
- `task docs:artifact:check` verifies the generated documentation data against
  its Markdown sources.
