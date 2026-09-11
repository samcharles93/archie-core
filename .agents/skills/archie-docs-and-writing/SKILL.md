---
name: archie-docs-and-writing
description: Maintain Archie's documentation of record and write durable architecture decisions, migration plans, parity matrices, incident and dead-end records, feature ownership and deprecation records, operational runbooks, and documentation reviews. Use when deciding where an Archie fact belongs; reconciling code with ARCHITECTURE.md, CLAUDE.md, docs/prds, docs/archive, or generated contracts; changing tools/docsgen; documenting a feature so future maintainers can find its owners, consumers, invariants, superseded paths, deletion gates, evidence, and rollback; or checking documentation authority, drift, links, copied content, and source-artifact hygiene.
---

# Maintain Archie documentation

Write one durable record for each decision or behavior. Separate what runs now
from what Archie has approved as its destination.

Route to `archie-architecture-planning-campaign` for ownership/boundary
decisions, `archie-architecture-contract` for invariants,
`archie-codebase-discovery` for live entry points,
`archie-failure-archaeology` for incidents,
`archie-config-and-flags`, `archie-build-and-env`, or `archie-run-and-operate`
for runtime/operations facts.

## Use the authority vocabulary

Define a **record of authority** as the one canonical location whose owner may
change a claim.

| State | Meaning | Permitted wording |
|---|---|---|
| `CURRENT` | Verified behavior in checked-out code, tests, composition, or executable config | "Currently does" |
| `APPROVED TARGET` | Normative destination approved in architecture index | "Must become"; never "currently does" |
| `OPEN` | Investigation, candidate, or unresolved decision | "Candidate", "unknown", or "requires decision" |
| `HISTORICAL` | Prior design, incident, dead end, or superseded choice | "Previously did" |
| `GENERATED` | Derivative output produced from owned source definition | "Generated from"; never hand-edit |
| `EXTERNAL` | Fact held outside repository, such as deployed config or host state | Date, name verifier, give re-check command |

When `CURRENT` conflicts with `APPROVED TARGET`, preserve both and state the
migration delta.

## Use the repository authority map

Verified on 2026-07-28:

| Location | Role | Discipline |
|---|---|---|
| Live Go code, tests, `Taskfile.yml`, executable config parsing, composition under `cmd/` | `CURRENT` execution evidence | Trace producers and consumers; tests prove only asserted behavior |
| `CLAUDE.md` | Current contributor and safety protocol plus compact architecture orientation | Verify operational claims against code and `Taskfile.yml`; `AGENTS.md` is a symlink |
| `ARCHITECTURE.md` | Useful architecture history and partial current overview | Corroborate every inventory/status claim; its "Planned" list is stale for implemented skill support |
| `docs/prds/01-project-management.md` | Index and approved foundation for target architecture | Add or change target decisions in the focused document it names |
| `docs/architecture/*.md` | Focused target decisions, active review procedure, migration inventory | Read each file's status |
| `docs/architecture/migration-decisions.md` | `OPEN` migration inventory constrained by approved decisions | Close a question only after code-grounded review |
| `docs/archive/` | `HISTORICAL` material | Mine rationale and failure evidence |
| `CHANGELOG.md`, `CHANGELOG.archied.md`, `CHANGELOG.archie.md` | Component release-note index and packaged runtime inputs | Keep gateway and agent-runtime entries separate; `internal/releaseannounce` parses version headings |
| `tools/docsgen` | Current, partial generator in nested `tools` Go module | Treat its flags and tests—not planned PRD commands—as executable truth |
| `docs/data/generated/contracts.json` | Current working-tree `GENERATED` output | Regenerate; never edit by hand |
| `docs/` | Repository documentation only | Nothing builds, renders, or publishes it; Markdown is the artifact |

The repository root has no `README.md` or `CONTRIBUTING.md` as of 2026-07-28.

## Choose one destination

1. State the reader's question.
2. Identify the owner of the behavior or decision.
3. Find the existing record of authority.
4. Extend that record or link to it. Do not make a second canonical table.
5. If no owner exists, route the ownership decision through the architecture campaign.
6. If the fact is generated, change the owned source definition and generator.
7. If the fact is external, date it and name the exact observation command.

| Content | Destination |
|---|---|
| Cross-cutting approved architecture index | `docs/prds/01-project-management.md` |
| Focused domain or requirement decision | Matching `docs/architecture/*.md` file |
| Unresolved migration question | `docs/architecture/migration-decisions.md` |
| Contributor protocol that must load before work | `CLAUDE.md` |
| Runtime/API/config reference derivable from code | Domain-owned registry/type, then `tools/docsgen` output |
| Repository documentation prose | `docs/`, linking back to authority with repository-relative paths |
| Shipped release behavior | Matching component changelog; keep `CHANGELOG.md` as two-component index |
| Superseded design retained for context | `docs/archive/`, with historical status |
| Incident or rejected approach | `archie-failure-archaeology`'s chronology |

Do not duplicate a package map across `CLAUDE.md`, `ARCHITECTURE.md`, a PRD, and
the site. Name the authoritative map and link to it.

## Write for future change

Every architecture, feature, or migration record must answer:

- **Responsibility:** What cohesive job exists?
- **Owner:** Which domain, application boundary, or infrastructure adapter owns
  its meaning and mutable state?
- **Boundaries:** What is explicitly inside and outside?
- **Consumers:** Which entry points, interfaces, commands, events, config, and
  processes depend on it?
- **Invariants:** What must remain true, and where is each enforced?
- **Superseded path:** Which older mechanism becomes redundant?
- **Deletion gate:** What observable proof permits removal?
- **Evidence:** Which symbols, tests, commands, and runtime observations support
  the record?
- **Rollback:** How can behavior and data return safely?
- **Unresolved questions:** What remains `OPEN`?

Name one owner for every mutable record. A new path is incomplete until the
document says whether each older path remains, delegates, migrates, or is
deleted.

## Cite evidence without freezing line numbers

Prefer: repository-relative path plus exact Go symbol, test name, config key,
workflow job/step, or Markdown heading; a relative Markdown link to the record
of authority; the command that re-discovers the evidence.

Write `internal/agentexec.Request` and `tools/docsgen.currentContractTypes`.
Distinguish evidence strength:

1. Runtime composition and executable behavior.
2. Tests that exercise the relevant production seam.
3. Approved target decision.
4. Comments and summaries corroborated by the first three.
5. Historical or external notes, explicitly dated.

## Maintain component changelogs

Keep gateway behavior in `CHANGELOG.archied.md` and agent-runtime behavior in
`CHANGELOG.archie.md`; keep `CHANGELOG.md` as their index. The archied image
copies both component files to `/usr/share/archie/`, and
`internal/releaseannounce.changelogSection` parses a version heading.

A verified 2026-07-28 contradiction is `OPEN`:

- `.gitea/workflows/deploy.yml` passes `vX.Y.Z` component versions.
- `changelogSection` searches for `## [<version>]`, and its fixtures use
  `## [v0.1.0]`.
- The two current component changelogs use headings such as `## [1.1.0]`
  without `v`.
- `cmd/archied.TestChangelogsTrackGatewayAndRuntimeIndependently` expects
  the unprefixed current heading.

Resolve parser, release metadata tests, and both documents atomically; do not
"fix" only the prose.

## Maintain generated contracts

Current behavior, verified 2026-07-28:

- Tool path is `tools/docsgen`, not `tools/cmd/docsgen`.
- Separate Go module replacing root module with `../`.
- Accepts `--repo-root` and `--out`; no implemented `data`, `asyncapi`, `all`,
  or `check` subcommands.
- `currentContractTypes` publishes three top-level contracts and collects
  referenced schemas into one JSON object.
- Default output is `docs/data/generated/contracts.json`.

```bash
GOTMPDIR=/tmp GOCACHE=/tmp/archie-docsgen-gocache go -C tools test ./docsgen -count=1
docs_tmp="$(mktemp /tmp/archie-contracts.XXXXXX.json)"
GOTMPDIR=/tmp GOCACHE=/tmp/archie-docsgen-gocache go -C tools run ./docsgen --repo-root .. --out "$docs_tmp"
cmp "$docs_tmp" docs/data/generated/contracts.json
```

Treat the PRD's `docsgen all`, `docsgen check`, and `task docs:*` commands as
`APPROVED TARGET`.

## Store documentation as repository Markdown

`docs/` is Markdown read directly in the repository, an editor, or the forge's
file view. Nothing builds, renders, or publishes it. The VitePress site, its
package manifest and lockfile, its landing page, and
`.github/workflows/docs.yml` were removed on 2026-09-12 as an unnecessary build
step; the publishing surface is an open decision
(`docs/architecture/generated-documentation.md`, "Rendering and publishing").

| Surface | Current state |
|---|---|
| Content | Markdown under `docs/`, including `docs/data/generated/contracts.json` |
| Links | Repository-relative (`../architecture/organisation.md`); no root-absolute links |
| Build | None. There is no site, no dev server, no link checker, no Pages deploy |
| Nav | `docs/prds/01-project-management.md` |

Because no build validates links, check them yourself or with a one-off script —
nothing catches a dead link for you:

```bash
# every relative markdown link target that does not resolve
grep -rhoE '\]\([^)#]+' docs --include=*.md | sed -E 's/^\]\(//' | sort -u
```

### Enforce source-artifact hygiene

Git tracks no `node_modules` entries (verified 2026-09-12; the 237 `docs/`
symlinks recorded on 2026-07-28 are gone) and no VitePress output. A future
renderer must not reintroduce committed installs or build output.

```bash
git ls-files | grep -c node_modules                  # expect 0
ls docs/package.json docs/pnpm-lock.yaml 2>&1        # expect: No such file
ls .github/workflows/                                # expect deploy.yml only
```

Historical mentions of VitePress in `docs/architecture/` are deliberate — they
record what was removed. A grep for the word alone is not a failure signal.

Never stage dependency installs, renderer output, generator scratch files,
credentials, absolute paths, wall-clock data, or machine-specific values.
