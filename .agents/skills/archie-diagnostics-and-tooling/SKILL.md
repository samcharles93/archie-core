---
name: archie-diagnostics-and-tooling
description: "Measure Archie codebase maintainability, Go package dependency shape, configuration consumption, production composition, test and delivery-gate drift, tracked-artifact hygiene, coverage, races, and runtime evidence. Load when establishing a dated baseline, finding large or complex functions, investigating duplicate or dead-path candidates, checking whether configuration reaches an entrypoint, comparing architecture before and after a refactor, or replacing an eyeballed quality claim with reproducible numbers."
---

# Measure Archie instead of eyeballing it

Collect evidence before judging quality. Keep collection read-only, save raw
output, state what each metric cannot prove, and route behavior-changing
decisions through the appropriate gate.

Volatile facts and numeric snapshots below were verified on **2026-07-28**.
Re-run the commands before using any number as a baseline.

## Scope and routing

Use this skill to answer measurable questions:

- Which production functions exceed the repository's size or complexity
  triage thresholds?
- Which packages have high internal fan-in or fan-out?
- Which tagged config fields have no syntax-level production or entrypoint
  selector evidence?
- Which checks appear literally in Task, CI, docs, and deploy surfaces?
- Did a refactor reduce a named hotspot without adding dependencies or losing
  validation coverage?

Do **not** use this skill as:

| Need | Load instead |
|---|---|
| Traverse an input through types, state, persistence, transports, consumers, and production composition | `archie-codebase-discovery` |
| Turn a hunch into a decision-gated experiment | `archie-research-methodology` |
| Decide package boundaries or run an architecture migration | `archie-architecture-planning-campaign` and `archie-architecture-contract` |
| Approve a patch or define acceptance evidence | `archie-validation-and-qa` and `archie-change-control` |
| Diagnose a live failure or operate a deployment | `archie-debugging-playbook` and `archie-run-and-operate` |
| Assign an owner, prove supersession, or approve deletion | `archie-technical-accountability` |
| Enumerate config defaults and supported axes | `archie-config-and-flags` |

Never call a path dead, a field unwired, or a package misplaced from one report.
Use reports to prioritize exact semantic traversal.

## Define the measurement terms

- **Baseline:** dated raw output tied to a commit, toolchain, command, and
  environment.
- **Candidate:** a machine-found item requiring semantic confirmation.
- **Production composition:** concrete construction selected by a shipped
  entrypoint. Archie's principal composition root is `cmd/archied/main.go`.
- **Fan-in:** number of packages in the same module that directly import a
  package.
- **Fan-out:** number of packages in the same module that a package directly
  imports.
- **Physical line:** a newline-delimited source line, including comments and
  blanks. It is not a statement count.
- **Syntactic complexity:** this skill's approximation: one plus `if`, loop,
  non-default case, communication clause, `&&`, and `||` nodes. It is not
  `gocyclo`, `gocognit`, or `cyclop`.
- **Acceptance evidence:** evidence sufficient to approve the intended
  behavior. Diagnostics are inputs to acceptance, not acceptance by themselves.

## Establish a reproducible snapshot

Run from the repository root. Keep caches and output outside the tree:

```sh
export LC_ALL=C
export GOCACHE=/tmp/archie-diagnostics-gocache
export GOTMPDIR=/tmp
export XDG_CACHE_HOME=/tmp/archie-diagnostics-xdg
export GOLANGCI_LINT_CACHE=/tmp/archie-diagnostics-lint
export GOPROXY=off
export GOFLAGS=-mod=readonly

skill=.claude/skills/archie-diagnostics-and-tooling
stamp=$(date -u +%Y%m%dT%H%M%SZ)
evidence_dir=/tmp/archie-diagnostics-"$stamp"
mkdir -p "$evidence_dir"

git rev-parse --verify HEAD >"$evidence_dir/revision.txt"
git status --short >>"$evidence_dir/revision.txt"
go version >"$evidence_dir/tools.txt"
gopls version >>"$evidence_dir/tools.txt"
golangci-lint version >>"$evidence_dir/tools.txt"
staticcheck -version >>"$evidence_dir/tools.txt"

go run "$skill/scripts/source-metrics.go" -root . >"$evidence_dir/source.tsv"
go run "$skill/scripts/config-use-candidates.go" -root . >"$evidence_dir/config.tsv"
"$skill/scripts/package-shape.sh" . >"$evidence_dir/packages-root.tsv"
"$skill/scripts/package-shape.sh" tools >"$evidence_dir/packages-tools.tsv"
"$skill/scripts/delivery-snapshot.sh" >"$evidence_dir/delivery.tsv"
```

`GOPROXY=off` and `-mod=readonly` prevent this evidence pass from fetching or
changing dependencies. Report an uncached dependency as an environment blocker.
The package helper preserves a harmless read-only module stat-cache warning but
normalizes its random temporary suffix to `<TEMP>`; preserve stderr and the exit
status with the report.

Write a measurement charter before interpreting output:

| Field | Required content |
|---|---|
| Question | One falsifiable maintainability, wiring, or delivery claim |
| Scope | Exact packages, modes, config branches, and modules |
| Predicted result | Expected baseline and before/after delta |
| Collection command | Exact command and environment |
| Branch condition | What contrary number triggers deeper discovery |
| Decision owner | Skill or reviewer that may approve action |

Do not invent a composite “quality score.” Keep size, complexity, dependency,
duplication, coverage, runtime, and ownership evidence separate.

## Use the bundled reports

### Measure source shape

Run:

```sh
go run "$skill/scripts/source-metrics.go" -root . -top 30
```

Read its tab-separated schema:

| Record | Columns after record name |
|---|---|
| `META` | key, value |
| `METRIC` | module, metric, integer |
| `PACKAGE` | module, directory, production files, test files, generated files, production lines, test lines, generated lines |
| `HOTSPOT` | module, file, declaration line, symbol, body-span lines, statement nodes, approximate syntactic complexity |

The script:

- scans repository-owned Go files;
- separates the root and `tools` modules;
- separates production, test, and generated files;
- excludes `.git`, `.claude`, `.references`, `node_modules`, `vendor`,
  `.gotmp`, and VitePress cache/output;
- sorts output deterministically;
- reports only production hotspots unless
  `-include-test-hotspots` is passed.

The defaults, 80 body lines and complexity 15, mirror two numeric triage points
in `.golangci.yml`. They do **not** implement `funlen` or `cyclop`. Run the
configured linters for authoritative linter findings:

```sh
golangci-lint run --enable-only=funlen,gocognit,gocyclo,cyclop,maintidx,nestif ./internal/...
```

If a hotspot shrinks, verify that responsibility moved to a cohesive owner.
Moving the same work into unowned helpers is not improvement.

### Measure package shape

Run both module reports:

```sh
"$skill/scripts/package-shape.sh" . >"$evidence_dir/packages-root.tsv"
"$skill/scripts/package-shape.sh" tools >"$evidence_dir/packages-tools.tsv"
awk -F '	' '$1=="PACKAGE" {print $3 "\t" $4 "\t" $2}' "$evidence_dir/packages-root.tsv" |
  sort -nr -k1,1 -k2,2 |
  sed -n '1,20p'
```

Read `PACKAGE package fan_in fan_out` and `EDGE from -> to` rows. The script
reuses
`.claude/skills/archie-codebase-discovery/scripts/package-edges.sh`; do not
create a second package-edge implementation.

Interpret high fan-in as a change-risk signal and high fan-out as an
orchestration signal, not as automatic defects. The report contains only
direct, intra-module imports. It does not show `tools` importing the root
module, interface dispatch, RPC relationships, string registrations, or state
ownership. A successful `go list` rejects Go import cycles; it does not prove
the target dependency doctrine.

When an edge looks wrong:

1. Run `archie-codebase-discovery` on the imported contract.
2. Identify the concrete production consumer.
3. Compare the current edge with `docs/architecture/`.
4. Route any boundary change through the architecture campaign.

### Find configuration-consumption candidates

Run:

```sh
go run "$skill/scripts/config-use-candidates.go" -root . >"$evidence_dir/config.tsv"
rg 'candidate-no-(selector-outside-config|entrypoint-selector)' "$evidence_dir/config.tsv"
```

Read each `CONFIG_FIELD` row as:

```text
path, line, Type.Field, tags, same-name definition count,
config-package selector count, cmd selector count,
other-production selector count, test selector count,
generated selector count, candidate status
```

The report finds named fields with `toml`, `yaml`, or `json` tags declared in
`internal/config`, then counts selectors with the same **spelling**. It is
deliberately fast and deliberately not type-aware.

For every candidate:

1. Inspect the declaration at the reported `path:line`.
2. Run `gopls references -d` on that exact field identifier.
3. Trace decode → default → validation → normalization/copy → composition-root
   read → concrete component → observable behavior.
4. Check reflection, serialization tags, Yaegi, registries, and string keys.
5. Separate feature-file tests from production selection.

Repeated names such as `Name`, `Command`, or `Budgets` have high false-positive
rates; use the same-name count as a warning. A zero `cmd` count may be correct
when the composition root passes an enclosing struct. A nonzero count may
belong to an unrelated field. Only type-aware references plus composition prove
wiring.

### Measure delivery and hygiene drift

Run:

```sh
"$skill/scripts/delivery-snapshot.sh" >"$evidence_dir/delivery.tsv"
```

Read:

- `SURFACE axis scope literal_count` as literal text evidence, not parsed YAML
  semantics;
- `HYGIENE metric count` as tracked-path candidates;
- `MODULE path` as discovered Go module boundaries.

The snapshot compares Task, GitHub docs CI, Gitea deploy CI, and selected
composition-root anchors. A zero says the literal pattern was absent. It does
not prove the behavior is absent from a called script or external system.

Inspect hygiene candidates directly:

```sh
git ls-files |
  rg '(^|/)(node_modules|bin|dist|\.gotmp)(/|$)|\.(exe|test|out)$'
git ls-files -s | awk '$1=="120000" {print}'
```

Do not delete a tracked candidate from a metric alone. Establish ownership,
generation source, consumers, deployment need, and a rollback path with
`archie-technical-accountability`.

## Investigate duplication and hidden dead paths

Measure textual duplication:

```sh
golangci-lint run --enable-only=dupl ./internal/...
```

Measure unused unexported declarations:

```sh
staticcheck -checks=U1000 ./internal/...
```

On 2026-07-28, the focused `dupl` command reported zero issues. That does not
contradict the known maintainability problem: two code paths can implement the
same responsibility with different syntax.

For a duplicate or one-shot candidate:

1. Name the externally visible operation, state transition, event subject, or
   side effect.
2. Search all entrypoints and modes for that operation.
3. Use `gopls call_hierarchy`, `gopls references -d`, and the discovery skill's
   AST construction scan.
4. Check interfaces, registrations, reflection, build tags, OS files, and wire
   names.
5. Identify what a new feature supersedes and the objective removal gate.
6. Record production-only, test-only, compatibility, generated, and
   unreachable paths separately.

As of 2026-07-28, `Taskfile.yml` defines no `deadcode` task and the installed
`go tool` lists no dead-code analyzer. Archived PRDs mentioning
`task deadcode` describe a desired or former command, not a live command.

## Collect test, race, coverage, and runtime evidence

Keep collection scoped to the hypothesis:

```sh
go test ./internal/nell -count=1 -coverprofile=/tmp/archie-nell.cover
go tool cover -func=/tmp/archie-nell.cover
go test -race ./internal/nell -count=1
```

These commands were verified on 2026-07-28; the package reported 65.2% statement
coverage and passed its race run. The percentage is volatile and is not a
quality threshold. Read uncovered functions and error paths.

`go test` can write CPU and memory profiles, but this checkout's `go tool`
listed no `pprof` frontend on 2026-07-28. Do not promise profile inspection
until `command -v pprof` or another approved viewer succeeds. Do not download a
viewer during a read-only evidence pass.

Collect container logs without following indefinitely:

```sh
docker compose logs archied >"$evidence_dir/archied.log"
docker compose logs nats >"$evidence_dir/nats.log"
curl -fsS http://localhost:8222/healthz >"$evidence_dir/nats-health.txt"
```

Run those only when the local Compose stack is in scope. Repository files do
not prove the remote `carina` instances match local Compose. `docker-compose.yml`
publishes NATS monitoring on port 8222 and uses `/healthz` in its healthcheck;
it does not establish a metrics endpoint contract. Route remote operations and
log interpretation through `archie-run-and-operate` and
`archie-debugging-playbook`.

Some tests bind loopback listeners, including email, forge, storage, Telegram,
web UI, and MCP tests. A sandbox refusal to listen is environment evidence, not
a code regression. Re-run in an allowed environment before classifying it.

## Compare before and after

Predict exact deltas before changing code. Example:

```text
Prediction:
- cmd/archied.run body span: 668 → below 350
- root package count: unchanged
- new internal package edges: zero unless named in the design
- config candidate statuses: no regressions
- Task/CI/docs surface counts: unchanged unless explicitly in scope
```

Collect a second snapshot with the same script revision, flags, environment,
and toolchain:

```sh
after_dir=/tmp/archie-diagnostics-after
mkdir -p "$after_dir"
go run "$skill/scripts/source-metrics.go" -root . >"$after_dir/source.tsv"
go run "$skill/scripts/config-use-candidates.go" -root . >"$after_dir/config.tsv"
"$skill/scripts/package-shape.sh" . >"$after_dir/packages-root.tsv"
"$skill/scripts/delivery-snapshot.sh" >"$after_dir/delivery.tsv"

diff -u "$evidence_dir/source.tsv" "$after_dir/source.tsv"
diff -u "$evidence_dir/config.tsv" "$after_dir/config.tsv"
diff -u "$evidence_dir/packages-root.tsv" "$after_dir/packages-root.tsv"
diff -u "$evidence_dir/delivery.tsv" "$after_dir/delivery.tsv"
```

If output differs from the prediction:

- confirm the checkout, dirty state, script revision, flags, and toolchain;
- explain each delta from changed source;
- branch to semantic discovery when a count cannot explain behavior;
- reject a “better” metric that moved duplication, hid a path, or weakened a
  gate;
- route the resulting change through validation and fresh adversarial review.

## Record the evidence packet

Report:

1. question and numeric prediction;
2. revision, dirty state, date, tools, and environment constraints;
3. exact commands and exit codes;
4. raw evidence paths;
5. before/after deltas by independent dimension;
6. semantic confirmation for every candidate used in a decision;
7. unexpected results and branch taken;
8. acceptance owner and remaining unknowns.

Short wins count only when they leave a reproducible vertical slice: one
measured problem, one explained change, and no displaced responsibility left
unowned.

## Dated current snapshot

The bundled scripts measured this checkout on 2026-07-28:

| Observation | Unstable value |
|---|---:|
| Root production/test/generated Go files | 123 / 111 / 4 |
| Root production/test/generated physical lines | 25,952 / 31,675 / 157 |
| `cmd/archied.run` body span / approximate complexity | 668 / 96 |
| `internal/config.finalize` body span / approximate complexity | 177 / 64 |
| Root internal packages / direct internal edges | 51 / 129 |
| Tagged config field rows | 110 |
| Tracked paths / tracked `node_modules` paths / tracked symlinks | 558 / 237 / 238 |
| `.dockerignore` present | 0 |

The delivery snapshot found no Taskfile literals for race, the `tools` module,
or docs; no Go-gate literals in either workflow; a docs build in GitHub CI; and
container build/push in Gitea CI. `task check` is mutating because it invokes
`gofumpt -w .` and `go fix ./...`; do not run it as a read-only diagnostic.
Route it to acceptance.

The focused test
`go test ./internal/skillscript -run '^TestRunWrapsExternalCommand$' -count=1 -v`
failed on 2026-07-28 with `Run() = "\n"`. Preserve that known-red baseline; do
not claim a clean full suite or attribute it to unrelated work.

## Provenance and maintenance

Ground truth verified on 2026-07-28:
`Taskfile.yml`, `.golangci.yml`, `go.mod`, `tools/go.mod`,
`cmd/{archied,archie-agent}/`, `internal/{config,nell,skillscript}/`,
`.github/workflows/docs.yml`, `.gitea/workflows/deploy.yml`,
`docker-compose.yml`, `docs/package.json`, and `tools/docsgen/`.

Re-verify scripts: `sh -n .claude/skills/archie-diagnostics-and-tooling/scripts/*.sh && go vet .claude/skills/archie-diagnostics-and-tooling/scripts/source-metrics.go && go vet .claude/skills/archie-diagnostics-and-tooling/scripts/config-use-candidates.go`

Re-verify source baseline: `GOCACHE=/tmp/archie-diagnostics-gocache GOTMPDIR=/tmp go run .claude/skills/archie-diagnostics-and-tooling/scripts/source-metrics.go -root . -top 12`

Re-verify config candidates: `GOCACHE=/tmp/archie-diagnostics-gocache GOTMPDIR=/tmp go run .claude/skills/archie-diagnostics-and-tooling/scripts/config-use-candidates.go -root .`

Re-verify package edges: `.claude/skills/archie-diagnostics-and-tooling/scripts/package-shape.sh .`

Re-verify delivery surfaces: `.claude/skills/archie-diagnostics-and-tooling/scripts/delivery-snapshot.sh`

Re-verify lint thresholds: `rg -n 'cyclop:|dupl:|funlen:|gocognit:|gocyclo:|maintidx:|nestif:' .golangci.yml`

Re-verify current test baseline: `GOCACHE=/tmp/archie-diagnostics-test-cache GOTMPDIR=/tmp GIT_CONFIG_GLOBAL=/dev/null GOPROXY=off GOFLAGS=-mod=readonly go test ./internal/skillscript -run '^TestRunWrapsExternalCommand$' -count=1 -v`
