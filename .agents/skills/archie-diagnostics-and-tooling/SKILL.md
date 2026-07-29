---
name: archie-diagnostics-and-tooling
description: "Measure Archie codebase maintainability, Go package dependency shape, configuration consumption, production composition, test and delivery-gate drift, tracked-artifact hygiene, coverage, races, and runtime evidence. Load when establishing a dated baseline, finding large or complex functions, investigating duplicate or dead-path candidates, checking whether configuration reaches an entrypoint, comparing architecture before and after a refactor, or replacing an eyeballed quality claim with reproducible numbers."
---

# Measure Archie instead of eyeballing it

Collect evidence before judging quality. Keep collection read-only, save raw
output, state what each metric cannot prove.

Volatile facts and numeric snapshots below were verified on **2026-07-28**.

Route to `archie-codebase-discovery` for semantic traces,
`archie-config-and-flags` for config enumeration,
`archie-debugging-playbook` for live failures.

## Define the measurement terms

- **Baseline:** dated raw output tied to a commit, toolchain, command, and
  environment.
- **Candidate:** a machine-found item requiring semantic confirmation.
- **Production composition:** concrete construction selected by a shipped
  entrypoint. Principal: `cmd/archied/main.go`.
- **Fan-in:** number of packages in the same module that directly import a
  package.
- **Fan-out:** number of packages in the same module that a package directly
  imports.
- **Physical line:** a newline-delimited source line, including comments and
  blanks.
- **Syntactic complexity:** this skill's approximation: one plus `if`, loop,
  non-default case, communication clause, `&&`, and `||` nodes.

## Establish a reproducible snapshot

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

## Use the bundled reports

### Measure source shape

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

The defaults (80 body lines, complexity 15) mirror numeric triage points in
`.golangci.yml`. They do **not** implement `funlen` or `cyclop`. Run configured
linters for authoritative findings:

```sh
golangci-lint run --enable-only=funlen,gocognit,gocyclo,cyclop,maintidx,nestif ./internal/...
```

If a hotspot shrinks, verify that responsibility moved to a cohesive owner.
Moving the same work into unowned helpers is not improvement.

### Measure package shape

```sh
"$skill/scripts/package-shape.sh" . >"$evidence_dir/packages-root.tsv"
"$skill/scripts/package-shape.sh" tools >"$evidence_dir/packages-tools.tsv"
awk -F '	' '$1=="PACKAGE" {print $3 "\t" $4 "\t" $2}' "$evidence_dir/packages-root.tsv" |
  sort -nr -k1,1 -k2,2 |
  sed -n '1,20p'
```

Read `PACKAGE package fan_in fan_out` and `EDGE from -> to` rows. High fan-in
signals change risk; high fan-out signals orchestration. The report contains
only direct, intra-module imports.

### Find configuration-consumption candidates

```sh
go run "$skill/scripts/config-use-candidates.go" -root . >"$evidence_dir/config.tsv"
rg 'candidate-no-(selector-outside-config|entrypoint-selector)' "$evidence_dir/config.tsv"
```

Read each `CONFIG_FIELD` row as path, line, Type.Field, tags, same-name
definition count, config-package selectors, cmd selectors, other-production
selectors, test selectors, generated selectors, candidate status. The report
finds named fields with `toml`/`yaml`/`json` tags in `internal/config`, then
counts selectors with the same **spelling**. It is deliberately fast and not
type-aware.

For every candidate: run `gopls references -d` on the exact field identifier;
trace decode → default → validation → normalization/copy → composition-root read
→ concrete component → observable behavior.

### Measure delivery and hygiene drift

```sh
"$skill/scripts/delivery-snapshot.sh" >"$evidence_dir/delivery.tsv"
```

Read: `SURFACE axis scope literal_count` as literal text evidence;
`HYGIENE metric count` as tracked-path candidates; `MODULE path` as discovered
Go module boundaries. The snapshot compares Task, GitHub docs CI, Gitea deploy
CI, and selected composition-root anchors.

Inspect hygiene candidates directly:

```sh
git ls-files |
  rg '(^|/)(node_modules|bin|dist|\.gotmp)(/|$)|\.(exe|test|out)$'
git ls-files -s | awk '$1=="120000" {print}'
```

## Investigate duplication and hidden dead paths

```sh
golangci-lint run --enable-only=dupl ./internal/...
```

```sh
staticcheck -checks=U1000 ./internal/...
```

On 2026-07-28, the focused `dupl` command reported zero issues. Two code paths
can implement the same responsibility with different syntax.

For every candidate: name the externally visible operation; search all
entrypoints and modes; use `gopls call_hierarchy`, `gopls references -d`, and
AST construction scan; check interfaces, registrations, reflection, build tags,
OS files, and wire names; identify what a new feature supersedes; record
production-only, test-only, compatibility, generated, and unreachable paths
separately.

As of 2026-07-28, `Taskfile.yml` defines no `deadcode` task and `go tool` lists
no dead-code analyzer.

## Collect test, race, coverage, and runtime evidence

```sh
go test ./internal/nell -count=1 -coverprofile=/tmp/archie-nell.cover
go tool cover -func=/tmp/archie-nell.cover
go test -race ./internal/nell -count=1
```

As of 2026-07-28, the package reported 65.2% statement coverage and passed its
race run.

```sh
docker compose logs archied >"$evidence_dir/archied.log"
docker compose logs nats >"$evidence_dir/nats.log"
curl -fsS http://localhost:8222/healthz >"$evidence_dir/nats-health.txt"
```

Run only when local Compose stack is in scope.

## Compare before and after

Predict exact deltas before changing code. Collect a second snapshot with the
same script revision, flags, environment, and toolchain:

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

If output differs from prediction: confirm checkout, dirty state, script
revision, flags, toolchain; explain each delta; branch to semantic discovery
when a count cannot explain behavior. Reject a "better" metric that moved
duplication, hid a path, or weakened a gate.

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
or docs; no Go-gate literals in either workflow; docs build in GitHub CI;
container build/push in Gitea CI.

The focused test
`go test ./internal/skillscript -run '^TestRunWrapsExternalCommand$' -count=1 -v`
failed on 2026-07-28 with `Run() = "\n"`.
