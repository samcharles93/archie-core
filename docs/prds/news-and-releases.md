# Public news and per-version release notes -- decision

**Status:** Approved
**Date:** 2026-09-21
**Beads issue:** `archie-core-2ncm.2`

A public news page and a release-notes page for every version. Both are derived
from the changelogs the release process already produces, so publishing a
release adds no writing. Part A is the derivation and the artifact any renderer
consumes; it is not tied to MkDocs. Part B is the MkDocs rendering that
publishes them at `offloaded.dev/news/`.

## Part A -- release facts and their derivation

### Source of truth

Release notes come from `CHANGELOG.archied.md` and `CHANGELOG.archie.md`. Those
are the release process's frozen notes: `tools/release.sh --prepare` writes a
section, the maintainer edits it, and `--tag` refuses to tag a version without
its section. A page exists exactly for a release the process cut, and no second
copy of release prose can drift from the changelog.

Tags are not the source. CI and deployment check out shallow, so tags are absent
from a site build; the changelogs are always present and their
`## [<version>] - <date>` headings carry the same version and date the tags
carry. Raw commits are not the source either: the changelog is the edited text,
not the commit list.

### What a version's notes contain

The version and date parsed from the section heading, and the section body
unchanged. The body keeps the maintainer's headings and bullets; the generator
does not regroup them into feature and fix categories, because those categories
exist only on commits the edited changelog does not preserve.

`## [Unreleased]` is skipped. Any other `## [` heading that is not
`[version] - date` stops generation, so a malformed heading cannot silently drop
a release. A relative Markdown link in a body stops generation too: a rendered
page sits at a different depth from the changelog, and a host's link checking
would fail on the dead link. Absolute links pass through.

### Two components

`archied` and `archie-agent` version independently and either can be held back,
so the data carries the tag prefix (`archied`, `archie`), the product name
(`archied`, `archie-agent`), and the version as separate fields. A release that
moved one component is one entry. The generator never pairs two component
versions or invents a combined product version.

### The canonical artifact

`docs/news/releases.json` is the host hand-off: a committed, generated,
newest-first array of `{component, name, version, date, body}`. Every adapter
renders from this file rather than re-parsing a changelog. The JSON is the
contract; a host adapter may add markup, but not facts.

### Zero per-release maintenance

`task news` regenerates the JSON and the MkDocs pages from the changelogs.
`task news:check` regenerates them in a temporary directory and fails when the
committed files differ; it runs in `task check`, so a changelog edit cannot land
without its regeneration. `tools/release.sh --tag` regenerates and stages them
in the release commit, so a tag's tree carries its own facts.

The generator owns `docs/news/releases.json`, `docs/news/index.md` and the two
component directories end-to-end: it rewrites expected files and reports an
unexpected file in a component directory as drift. It leaves every other file
under `docs/news/` alone, so a future hand-written announcement is not its
business.

## Part B -- publishing

MkDocs is the host. `docs/news/index.md` is the news page: date-grouped, newest
first, one entry per release carrying the component, the version, and a link to
its notes. `docs/news/archied/<version>.md` and `docs/news/archie/<version>.md`
hold the notes. `site_url` is `https://offloaded.dev/`, so the pages are served
at `offloaded.dev/news/`.

The version pages stay out of the derived navigation (`not_in_nav` in
`mkdocs.yml`) while remaining published; the news page is their entry point, so
a version list that grows with every release cannot push the architecture tree
off the screen.

A different renderer would read `docs/news/releases.json` instead of these
Markdown files. That is a separate change in that renderer's repository and
does not alter Part A; the JSON, not the Markdown, is the reusable half.

## Not in scope

- Generated summaries, categories, or section headings; the changelog has no
  structured summary field to derive one from.
- Per-release download or install links; where to get the artifacts is
  documented once, not restated for every version.
- Hand-written news items. The generated page lists releases.
- Changes to `infra/ansible`. Serving the site at a hostname other than
  `offloaded.dev` is an infrastructure decision.

## Verification

- `go test ./tools/newsgen/...` covers parsing, ordering, rendering, the link
  and heading refusals, and check-mode drift.
- `task news:check` passes inside `task check`.
- `task docs:site` builds the MkDocs adapter under `--strict`, which fails on a
  dead internal link.
