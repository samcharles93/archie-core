---
name: archie-docs-and-writing
description: Maintain Archie's documentation of record and write durable architecture decisions, migration plans, parity matrices, incident and dead-end records, feature ownership and deprecation records, operational runbooks, and documentation reviews. Use when deciding where an Archie fact belongs; reconciling code with ARCHITECTURE.md, CLAUDE.md, docs/prds, docs/archive, generated contracts, or the docs-2 VitePress site; changing tools/docsgen; documenting a feature so future maintainers can find its owners, consumers, invariants, superseded paths, deletion gates, evidence, and rollback; or checking documentation authority, drift, links, copied content, and source-artifact hygiene.
---

# Maintain Archie documentation

Write one durable record for each decision or behavior. Separate what runs now
from what Archie has approved as its destination. Make a future feature change
possible without rediscovering every code path.

## Route work before writing

Do not use this skill to make the underlying decision, investigate an incident,
or certify a code change:

| Need | Load instead |
|---|---|
| Decide package ownership, boundaries, or migration order | `archie-architecture-planning-campaign` |
| Check a load-bearing invariant | `archie-architecture-contract` |
| Map live entry points, consumers, and call paths | `archie-codebase-discovery` |
| Establish feature ownership, supersession, or deletion accountability | `archie-technical-accountability` |
| Reconstruct an incident, revert, or rejected fix | `archie-failure-archaeology` |
| Change configuration, build, deployment, or operations | `archie-config-and-flags`, `archie-build-and-env`, or `archie-run-and-operate` |
| Gate or accept a change | `archie-change-control` and `archie-validation-and-qa` |
| Turn an unproven hypothesis into evidence | `archie-research-methodology` |

Return here after those skills establish the facts or decision. Record their
result without broadening it.

## Use the authority vocabulary

Define a **record of authority** as the one canonical location whose owner may
change a claim. Label every material statement with one of these states:

| State | Meaning | Permitted wording |
|---|---|---|
| `CURRENT` | Verified behavior in the checked-out code, tests, composition, or executable config | “Currently does” |
| `APPROVED TARGET` | Normative destination approved in the architecture index | “Must become”; never “currently does” |
| `OPEN` | Investigation, candidate, or unresolved decision | “Candidate”, “unknown”, or “requires decision” |
| `HISTORICAL` | Prior design, incident, dead end, or superseded choice | “Previously did”; never normative |
| `GENERATED` | Derivative output produced from an owned source definition | “Generated from”; never hand-edit |
| `EXTERNAL` | Fact held outside this repository, such as deployed config or host state | Date, name the verifier, and give a re-check command |

When `CURRENT` conflicts with `APPROVED TARGET`, preserve both and state the
migration delta. Do not rewrite the target to match accidental code. Do not
describe target behavior as implemented merely because a PRD uses normative
language.

## Use the repository authority map

Treat this as a volatile map verified on 2026-07-28:

| Location | Role | Discipline |
|---|---|---|
| Live Go code, tests, `Taskfile.yml`, executable config parsing, and composition under `cmd/` | `CURRENT` execution evidence | Trace producers and consumers; tests prove only their asserted behavior |
| `CLAUDE.md` | Current contributor and safety protocol plus a compact architecture orientation | Verify operational claims against code and `Taskfile.yml`; `AGENTS.md` is a symlink to this file |
| `ARCHITECTURE.md` | Useful architecture history and partial current overview | Corroborate every inventory/status claim; its “Planned” list is stale for implemented skill support |
| `docs/prds/01-project-management.md` | Index and approved foundation for the target architecture | Add or change target decisions in the focused document it names |
| `docs/prds/architecture/*.md` | Focused target decisions, active review procedure, and migration inventory | Read each file's status; distinguish fixed decisions from explicitly remaining decisions |
| `docs/prds/architecture/migration-decisions.md` | `OPEN` migration inventory constrained by approved decisions | Close a question only after code-grounded review and an approved record |
| `docs/archive/` | `HISTORICAL` material | Mine rationale and failure evidence; never restore authority from a title such as “Final” |
| `CHANGELOG.md`, `CHANGELOG.archied.md`, `CHANGELOG.archie.md` | Component release-note index and packaged runtime inputs | Keep gateway and agent-runtime entries separate; `internal/releaseannounce` parses the version headings |
| `tools/docsgen` | Current, partial generator implementation in the nested `tools` Go module | Treat its flags and tests—not planned PRD commands—as executable truth |
| `docs-2/data/generated/contracts.json` | Current working-tree `GENERATED` output | Regenerate; never edit by hand; re-check whether it is tracked |
| `docs-2/` | Staging VitePress site | Do not treat a rendered summary as the architecture record |
| `.github/workflows/docs.yml` | Current Pages build/deploy workflow | It builds the site; it does not currently run Go setup, generator tests, or drift checking |

The repository root has no `README.md` or `CONTRIBUTING.md` as of 2026-07-28.
Record this as an onboarding coverage gap, not permission to invent a new
authority. `docs-2/README.md` is excluded by VitePress and still contains copied
Tau material; it is not a root onboarding guide.

## Choose one destination

1. State the reader's question in one sentence.
2. Identify the owner of the behavior or decision.
3. Find the existing record of authority.
4. Extend that record or link to it. Do not make a second canonical table.
5. If no owner exists, stop and route the ownership decision through
   `archie-technical-accountability` or the architecture campaign.
6. If the fact is generated, change the owned source definition and generator;
   do not patch output.
7. If the fact is external, date it and name the exact observation command.

Use this placement guide:

| Content | Destination |
|---|---|
| Cross-cutting approved architecture index | `docs/prds/01-project-management.md` |
| Focused domain or requirement decision | The matching `docs/prds/architecture/*.md` file |
| Unresolved migration question or cutover requirement | `docs/prds/architecture/migration-decisions.md` |
| Contributor protocol that must load before work | `CLAUDE.md` |
| Runtime/API/config reference derivable from code | Domain-owned registry/type, then `tools/docsgen` output |
| User-facing staging site prose | `docs-2/`, linking back to its authority |
| Shipped release behavior | The matching component changelog; keep `CHANGELOG.md` as the two-component index |
| Superseded design retained only for context | `docs/archive/`, with historical status |
| Incident or rejected approach | `archie-failure-archaeology`'s authoritative chronology |

Do not duplicate a package map in `CLAUDE.md`, `ARCHITECTURE.md`, a PRD, and the
site. Name the authoritative map and link to it.

## Research before drafting

Run from the repository root:

```bash
rg -n 'ExactTerm|RelatedTerm' --glob '*.go' --glob '*.md' --glob '*.toml' --glob '*.yml'
rg -n 'type ExactType|func .*ExactMethod|ExactConfigField' cmd internal domain tools
rg -n 'ExactType|ExactMethod' --glob '*_test.go'
rg -n 'ExactTerm' docs/prds ARCHITECTURE.md CLAUDE.md docs/archive docs-2
```

Then build a fact ledger:

| Claim | State | Owner | Evidence | Conflicts | Destination |
|---|---|---|---|---|---|
| One precise claim | `CURRENT` / `APPROVED TARGET` / `OPEN` | Domain or boundary | `path` + symbol/test/heading | Other path or none | Existing authority |

Investigate entry points, runtime composition, persistence, configuration,
consumers, tests, failure behavior, duplicate implementations, and stale docs.
Do not ask the user a question that repository evidence can answer.

## Write for future change

Every architecture, feature, or migration record must answer:

- **Responsibility:** What cohesive job exists?
- **Owner:** Which domain, application boundary, or infrastructure adapter owns
  its meaning and mutable state?
- **Boundaries:** What is explicitly inside and outside?
- **Consumers:** Which entry points, interfaces, commands, events, config, and
  processes depend on it?
- **Invariants:** What must remain true, and where is each invariant enforced?
- **Superseded path:** Which older mechanism becomes redundant?
- **Deletion gate:** What observable proof permits removal, and what temporary
  compatibility has an expiry condition?
- **Evidence:** Which symbols, tests, commands, and runtime observations support
  the record?
- **Rollback:** How can behavior and data return safely?
- **Unresolved questions:** What remains `OPEN`, who owns the decision, and what
  evidence will close it?

Name one owner for every mutable record. Name every current code path that
performs the same operation. A new path is incomplete until the document says
whether each older path remains, delegates, migrates, or is deleted.

Use [references/templates.md](references/templates.md) when drafting an ADR,
migration/parity plan, incident/dead-end record, feature ownership/deprecation
record, operational runbook, or docs review. Copy only the matching template;
delete prompts that do not apply rather than filling them with ceremony.

## Cite evidence without freezing line numbers

Prefer:

- repository-relative path plus exact Go symbol, test name, config key, workflow
  job/step, or Markdown heading;
- a relative Markdown link to the record of authority;
- the command that re-discovers the evidence.

Write `internal/agentexec.Request` and `tools/docsgen.currentContractTypes`, for
example. Avoid permanent `file.go:123` citations: line numbers drift without
changing meaning. Use line numbers in a time-bounded review comment only.

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
`internal/releaseannounce.changelogSection` parses a version heading to build
Telegram update messages.

Do not copy the current heading format without investigation. A verified
2026-07-28 contradiction is `OPEN`:

- `.gitea/workflows/deploy.yml` passes `vX.Y.Z` component versions.
- `changelogSection` searches for `## [<version>]`, and its fixtures use
  `## [v0.1.0]`.
- The two current component changelogs use headings such as `## [1.1.0]`
  without `v`.
- `cmd/archied.TestChangelogsTrackGatewayAndRuntimeIndependently` explicitly
  expects the unprefixed current heading.

Resolve the parser, release metadata tests, and both documents atomically
through `archie-change-control`; do not “fix” only the prose. Until resolved,
do not claim that a current component entry is parseable by the deployed
release announcer.

## Maintain generated contracts

The current implementation is smaller than the approved target in
`docs/prds/architecture/generated-documentation.md`.

### Current behavior, verified 2026-07-28

- The tool path is `tools/docsgen`, not `tools/cmd/docsgen`.
- It is a separate Go module that replaces the root module with `../`.
- It accepts `--repo-root` and `--out`; it has no implemented `data`,
  `asyncapi`, `all`, or `check` subcommands.
- `currentContractTypes` explicitly publishes three top-level contracts and
  collects their referenced schemas into one JSON object.
- Its default output is `docs-2/data/generated/contracts.json`.
- The current test proves byte-identical repeated output, expected top-level
  contracts, resolvable local references, duplicate/conflict rejection, and
  filesystem/source error reporting.
- The output in this working tree matches a fresh temporary generation, but
  `git ls-files` reports it as untracked. Re-check before relying on it in CI.

Test and compare without changing the repository:

```bash
GOTMPDIR=/tmp GOCACHE=/tmp/archie-docsgen-gocache go -C tools test ./docsgen -count=1
docs_tmp="$(mktemp /tmp/archie-contracts.XXXXXX.json)"
GOTMPDIR=/tmp GOCACHE=/tmp/archie-docsgen-gocache go -C tools run ./docsgen --repo-root .. --out "$docs_tmp"
cmp "$docs_tmp" docs-2/data/generated/contracts.json
```

Exit status `0` from `cmp` means the one current output matches. This is not a
complete obsolete-file, site-route, or target-registry drift check.

Regenerate the working-tree output only when the change is classified and
authorized:

```bash
GOTMPDIR=/tmp GOCACHE=/tmp/archie-docsgen-gocache go -C tools run ./docsgen --repo-root ..
```

Treat the PRD's `docsgen all`, `docsgen check`, and `task docs:*` commands as
`APPROVED TARGET`. Do not run or document them as current until their CLI and
Taskfile entries exist and tests prove them.

## Maintain the VitePress staging site

Current behavior, verified 2026-07-28:

| Surface | Current state |
|---|---|
| Package | `docs-2/package.json`; VitePress `^1.6.4`; pnpm lockfile resolves 1.6.4 |
| Site config | Archie title, local search, clean URLs, last-updated metadata, output at `.vitepress/dist` |
| Published prose | Handwritten home and architecture pages; no generated-data loader is present |
| Copied content | `docs-2/README.md` describes Tau and is excluded by `srcExclude` |
| Workflow | Frozen-lockfile install, `pnpm build`, Pages artifact upload/deploy |
| Versioning | pnpm major `10`; Node is `latest`, so the Node input is volatile |
| Missing gates | No Go setup, docsgen test, generator drift check, or root `task check` integration |

Run the implemented site commands for review and acceptance:

```bash
pnpm --dir docs-2 install --frozen-lockfile
pnpm --dir docs-2 build
```

Use `build` for review evidence.

### Interactive authoring (not for automated acceptance)

```bash
pnpm --dir docs-2 dev
```

This starts a long-running dev server at `http://localhost:5173` (by default).
Only use it for interactive authoring; stop it with `Ctrl-C` before proceeding.
It is not an acceptance gate. Record the exact pnpm and Node
versions in failure reports because CI currently permits version drift.

### Enforce source-artifact hygiene

As of 2026-07-28, Git tracks 237 `docs-2/node_modules` symlink entries.
Neither `docs-2/node_modules` nor `docs-2/.vitepress/dist` is ignored by the
root `.gitignore`. Treat cleanup and ignore changes as a classified repository
change; route them through `archie-change-control`.

Before review, inspect rather than assuming:

```bash
git ls-files -s 'docs-2/node_modules/**' | awk '$1 == 120000 { count++ } END { print count + 0 }'
git ls-files 'docs-2/.vitepress/dist/**'
git check-ignore -v docs-2/node_modules docs-2/.vitepress/dist || true
rg -n -i 'tau|template|placeholder' docs-2 --glob '!node_modules/**'
```

Never stage dependency installs, VitePress output, generator scratch files,
credentials, absolute paths, wall-clock data, or machine-specific values.

## Review a documentation change

Require all of the following:

- [ ] The document names its state, date, scope, owner, and record of authority.
- [ ] `CURRENT`, `APPROVED TARGET`, `OPEN`, `HISTORICAL`, `GENERATED`, and
  `EXTERNAL` claims are not blended.
- [ ] Every code claim has a path plus symbol, test, config key, or workflow
  step that can be re-discovered.
- [ ] The writer traced consumers and runtime composition, not just type
  declarations.
- [ ] The document states boundaries, invariants, superseded paths, deletion
  gates, evidence, rollback, and unresolved questions where applicable.
- [ ] No authoritative table or definition is copied from another owner.
- [ ] Generated files were regenerated, not hand-edited.
- [ ] Release notes use the correct component file, and release metadata,
  parser behavior, tests, and version headings agree.
- [ ] Generator tests and a non-mutating drift comparison pass.
- [ ] `pnpm --dir docs-2 build` passes for site changes.
- [ ] Copied product text and tracked build/dependency artifacts are absent from
  the intended change.
- [ ] Behavior-changing decisions passed `archie-change-control` and
  `archie-validation-and-qa`.

Reject “the code is self-explanatory,” “temporary compatibility” without a
deletion gate, and status-free future tense. A durable record must let a new
engineer determine what exists, what should replace it, and how to prove the
transition.

## Provenance and maintenance

Last verified against the checked-out repository on 2026-07-28. Re-run these
one-line checks before relying on volatile facts:

```bash
test -L AGENTS.md && readlink AGENTS.md && test ! -e README.md && test ! -e CONTRIBUTING.md
```

```bash
rg -n '^\\*\\*Status:\\*\\*|^# ' docs/prds/01-project-management.md docs/prds/architecture/*.md docs/archive/*.md
```

```bash
rg -n 'docsgen|docs:|pnpm|vitepress|CHANGELOG' Taskfile.yml .github/workflows/docs.yml tools/docsgen docs-2/package.json docs-2/.vitepress/config.mts Dockerfile.archied internal/releaseannounce cmd/archied
```

```bash
GOTMPDIR=/tmp GOCACHE=/tmp/archie-docsgen-gocache go -C tools test ./docsgen -count=1
```

```bash
docs_tmp="$(mktemp /tmp/archie-contracts.XXXXXX.json)"; GOTMPDIR=/tmp GOCACHE=/tmp/archie-docsgen-gocache go -C tools run ./docsgen --repo-root .. --out "$docs_tmp" && cmp "$docs_tmp" docs-2/data/generated/contracts.json
```

```bash
git status --short -- docs-2 tools docs/prds/architecture/generated-documentation.md .github/workflows/docs.yml; git ls-files -s 'docs-2/node_modules/**' | awk '$1 == 120000 { count++ } END { print count + 0 }'
```
