---
name: archie-codebase-discovery
description: "Trace Archie behavior and blast radius through Go source, types, state, persistence, transports, consumers, tests, configuration, production composition, and target architecture. Load before changing or planning a non-trivial feature, refactor, config field, interface, persistence path, workflow, channel, plugin, or agent execution path; also load to investigate duplicate behavior, hidden dead code, constructor bypasses, package dependencies, decoded-but-unwired configuration, or uncertainty about what a symbol actually affects."
---

# Discover the Archie codebase

Use evidence from the current checkout to explain a behavior before changing it.
Produce a trace that another engineer can reproduce. Do not stop at "tests pass"
or "grep found one call."

Volatile tool facts below were verified on **2026-07-28**. Re-check them before
depending on exact versions or subcommands.

## Scope and routing

This skill is read-only. Use it to establish what exists, who owns state, which
paths are live, and what a proposed change would displace.

Do **not** use it as:

| Need | Load instead |
|---|---|
| Assign ownership, justify deletion, or record cleanup debt | `archie-technical-accountability` |
| Design boundaries, packages, migrations, or a future architecture | `archie-architecture-planning-campaign` |
| Collect runtime, coverage, complexity, latency, or production measurements | `archie-diagnostics-and-tooling` |
| Turn a hypothesis into a controlled evidence campaign | `archie-research-methodology` |
| Learn NellDB's API, document model, MVCC, HLC, or persistence semantics | `archie-nelldb` |
| Implement or approve a behavior-changing patch | Route the resulting trace through `archie-change-control` |

Do not delete, move, or redesign code while running this skill. A discovery can
recommend a next investigation, but an ownership or architecture decision needs
the sibling skill named above.

## Define the evidence terms

- **Composition root:** executable code that selects concrete implementations
  and connects them. Archie's principal composition root is
  `cmd/archied/main.go`; the sandbox worker has its own root under
  `cmd/archie-agent/`.
- **Production wiring:** a path selected by a shipped entrypoint under an actual
  config/mode branch. A constructor, decoder, or passing unit test is not wiring.
- **State owner:** the component with authority to validate and persist a state
  transition, not every component that holds a copy.
- **Target PRD:** an approved description under `docs/prds/` of where the system
  should go. It is not proof that current code works that way.
- **Candidate:** a result from text or syntax search that still requires
  type-aware or composition-root confirmation.

## Establish a reproducible baseline

Run from the repository root:

```sh
go version
go env GOMOD GOWORK GOOS GOARCH
gopls version
golangci-lint version
staticcheck -version
rg --version | sed -n '1p'
```

Record missing tools instead of silently substituting a weaker claim. As of
2026-07-28 this checkout had Go 1.26.5, gopls 0.23.0,
golangci-lint 2.12.2, staticcheck 2026.1 (v0.7.0), and `rg`. `go tool` listed
no `callgraph` command. Do not write `go tool callgraph`; use
`gopls call_hierarchy`.

Use writable caches when the host's default Go caches are read-only:

```sh
export GOCACHE="${TMPDIR:-/tmp}/archie-discovery-gocache"
export XDG_CACHE_HOME="${TMPDIR:-/tmp}/archie-discovery-xdg"
export GOLANGCI_LINT_CACHE="${TMPDIR:-/tmp}/archie-discovery-lint"
export GOTMPDIR="${TMPDIR:-/tmp}"
export GOPROXY=off
export GOFLAGS=-mod=readonly
```

`GOPROXY=off` prevents discovery from fetching dependencies. If an uncached
module is required, report that as an environment blocker; do not turn a
read-only investigation into dependency mutation.

Inventory module boundaries before interpreting `./...`:

```sh
find . \( -path './.git' -o -path './.references' -o -path './.claude/worktrees' -o -name node_modules \) -prune -o -name go.mod -print
go list -m -f '{{.Path}} {{.GoVersion}}'
go -C tools list -m -f '{{.Path}} {{.GoVersion}}'
```

The root module and `tools/` are separate modules as of 2026-07-28. A root
`go test ./...` or `go list ./...` does not validate `tools/`.

## Build the behavior trace

Write the question first:

```text
Behavior:
Observable input:
Expected externally visible output:
State that may change:
Modes/configurations to compare:
```

Then traverse every row in order. Mark a row `N/A` only with evidence.

| Step | Find | Required evidence |
|---:|---|---|
| 1 | Entrypoint | `main`, handler, poller, RPC subject, or tool registration that admits the input |
| 2 | Construction | Constructor and every direct struct literal that can create the component |
| 3 | Contract | Interface, wire type, command, event, or public method actually crossed |
| 4 | Implementation | All type-valid implementations, then the one selected in each relevant mode |
| 5 | State owner | The function that validates the transition and owns concurrency/transaction rules |
| 6 | Persistence | Store adapter, schema/key/document, serialization, and reload/recovery path |
| 7 | Event/transport | Publish/send and subscribe/receive sides, subject/framing, timeout, retry, and acknowledgement |
| 8 | Consumer | Callers and downstream readers, including UI, channel, daemon, agent, and reconciliation paths |
| 9 | Tests | Unit, integration, contract, architecture, and production-composition tests; identify absent layers |
| 10 | Configuration | Decode, default, validation, normalization/copy, composition-root consumption, and deploy passthrough |
| 11 | Target architecture | Current-to-target mapping in `docs/prds/`; label gaps instead of describing target text as current |

Start at these verified anchors:

| Concern | Current anchor |
|---|---|
| Resident daemon | `cmd/archied/main.go` → `run()` → `daemon.Daemon` |
| Sandboxed worker | `cmd/archie-agent/main.go`, `cmd/archie-agent/taskrun.go` |
| Task orchestration | `internal/daemon/daemon.go`, `internal/workflow/` |
| Task contracts and SQLite | `internal/store/interface.go`, `internal/store/` |
| Production task persistence | `internal/nell/adapter.go`; selected by `nell.OpenStore` in `cmd/archied/main.go` |
| RPC split | `internal/{nats,taskrun,natsrpc,storerpc,forgerpc,worktreerpc}/` |
| Chat and channels | `internal/gateway/`, `internal/channels/` |
| Configuration | `internal/config/`, `config.example.toml`, `config.docker.toml` |
| Production/deploy shape | `cmd/archied/main.go`, `docker-compose.yml`, `.gitea/workflows/deploy.yml` |
| Target map | `docs/prds/01-project-management.md` and linked `docs/architecture/*.md` |

Do not assume this table is complete after code moves. Re-find anchors with
`go list` and `gopls`.

## Use syntax, type, and call evidence

### Map packages before reading files

List source and test files as the Go tool sees them:

```sh
go list -f '{{.ImportPath}}|go={{join .GoFiles ","}}|tests={{join .TestGoFiles ","}}|imports={{join .Imports ","}}' ./internal/store
go list -json ./internal/store
```

Map internal dependency direction:

```sh
.claude/skills/archie-codebase-discovery/scripts/package-edges.sh ./internal/...
```

Treat imports as dependency evidence, not ownership proof. Compare the result
with `docs/architecture/dependencies-and-contracts.md` only after
distinguishing current from target.

### Resolve symbols with gopls

Use a 1-indexed `file:line:column` position on the identifier:

```sh
gopls symbols internal/store/interface.go
gopls definition cmd/archied/main.go:154:18
gopls references -d internal/store/interface.go:25:2
gopls implementation internal/store/interface.go:16:6
gopls call_hierarchy internal/store/interface.go:25:2
```

Run all four queries together:

```sh
.claude/skills/archie-codebase-discovery/scripts/trace-go-position.sh internal/store/interface.go:25:2
```

Use `gopls implementation` on interfaces and interface methods. Use
`gopls references -d` on struct fields, functions, constants, and types.
`gopls call_hierarchy` exposes callers missed by a search for one textual call
shape. Read the resolved bodies; tool output is an index, not the explanation.

Inspect exported contracts and available tests:

```sh
go doc ./internal/store.TaskStore
go test ./internal/store -list '.'
rg -n '^func Test|^func Benchmark|^func Fuzz' internal/store
```

`go test -list` compiles the package and lists matching top-level tests; it does
not execute them.

### Generate AST candidates for construction

Find type declarations, direct composite literals, and functions returning a
named type:

```sh
.claude/skills/archie-codebase-discovery/scripts/scan-go-type-syntax.sh Daemon .
.claude/skills/archie-codebase-discovery/scripts/scan-go-type-syntax.sh -tests Task .
```

The helper parses Go ASTs; it does not infer the package identity of an
unqualified name. Treat results as candidates, then confirm each with
`gopls definition` or `gopls references`. Production-only output is the default;
pass `-tests` to expose test-only construction.

## Run focused discovery recipes

### Find duplicate behavior paths

1. Run the configured duplicate-code detector:

   ```sh
   golangci-lint run --enable-only=dupl ./internal/...
   ```

2. Search for the externally visible operation, status, subject, or log phrase:

   ```sh
   rg -n 'Transition\(|PublishTask\(|ClaimNext\(|StatusWaitingHuman' cmd internal
   ```

3. Trace each candidate back to an entrypoint and forward to its state owner.
4. Compare mode branches: in-process, subprocess, NATS/container,
   single-identity, and multi-identity where applicable.
5. Report semantic duplicates even when the code is textually different.
6. Do not call compatibility paths obsolete until production composition,
   deployed config, persistence compatibility, and target removal criteria agree.

`dupl` finds similar syntax, not two different implementations of one
responsibility.

### Find unused-but-compiled code

Run the available unexported-code check:

```sh
staticcheck -checks=U1000 ./internal/...
```

For every suspect:

1. Run `gopls references -d` on the declaration.
2. Search registration tables, `init`, reflection/Yaegi symbols, templates,
   JSON/TOML/YAML tags, RPC subjects, plugin manifests, and string dispatch.
3. Check build tags and OS-specific files from `go list -json`.
4. Search both executable composition roots.
5. Separate production, test-only, compatibility, generated, and unreachable
   paths.

`U1000` does not prove exported code, reflection targets, wire names, or
registrations are dead. Zero type references does not prove a string-addressed
capability is dead. Route any deletion decision to
`archie-technical-accountability`.

### Find interface implementations

1. Put the cursor position on the interface name and run
   `gopls implementation`.
2. Put it on each important method and run `gopls implementation` again.
3. Find compile-time assertions:

   ```sh
   rg -n 'var _ .*=' --glob '*.go' cmd internal
   ```

4. Trace which implementation the composition root selects for each mode.
5. Inspect test doubles separately; they establish substitutability only for
   exercised behavior.

An interface with three valid implementers still has only the implementation
selected by `cmd/archied` on a given production branch.

### Find struct-field consumers

1. Run `gopls references -d` on the **field declaration**, not its common name.
2. Trace assignments into the field and reads out of it separately.
3. Follow copies and normalization methods such as `ForTask`/`ToConfig`.
4. Check JSON/TOML/YAML tags and reflection separately; gopls cannot turn a
   decoder's string key into a field reference.
5. Compare production and `_test.go` references.

A field read only while constructing an unused object is not an effective
consumer.

### Detect constructor bypasses

1. Run `scan-go-type-syntax.sh Type .` without tests.
2. Inspect every `literal` outside the owning package.
3. Compare required validation/defaulting in constructors with direct literals.
4. Repeat with `-tests`; record tests that exercise impossible production
   states.
5. Use `gopls references` on the constructor and type to catch aliases and
   indirect uses the syntax helper cannot resolve.

Do not prohibit literals mechanically. Composition roots and test fixtures may
need them, but bypassed invariants must be explicit.

### Prove package dependency direction

1. Run `package-edges.sh` for the smallest package pattern.
2. Run `go list -deps -json <pattern>` when standard-library and third-party
   edges matter.
3. Identify cycles prevented by interfaces, adapters, or RPC packages.
4. Compare current edges with the target dependency rules.
5. Treat a new shared package as an architecture decision, not a quick cycle fix.

### Find decoded-but-unused configuration

For each field, prove this chain:

```text
file/key → decoder → default → validation → normalization/copy
→ composition-root read → concrete component → observable behavior
```

Use:

```sh
rg -n 'toml:"|yaml:"|json:"' internal/config
rg -n 'LoadOverlay|LoadDir|finalize|ForTask|ToConfig' internal/config cmd
```

Then run `gopls references -d` on the exact field. Check
`config.example.toml`, `config.docker.toml`, `docker-compose.yml`, config tests,
and `cmd/archied/main.go`.

As of 2026-07-28 `cmd/archied` calls `config.LoadOverlay`; the separate
`config.LoadDir` feature-file loader exists and has tests but was not selected
by that production entrypoint. Re-verify this before using the fact. Decoder
coverage proves accepted syntax, not deployed behavior.

### Prove production composition

1. Start at `cmd/archied/main.go:run`, not at the package under investigation.
2. Record the config condition for each branch.
3. Follow concrete construction into `daemon.Daemon` and gateway start
   functions.
4. For container/NATS behavior, also trace `cmd/archie-agent`.
5. Trace both sides of every RPC/transport and its error/timeout path.
6. Read contract/architecture tests, including files matching
   `*contract_test.go` and `*architecture_test.go`.
7. Confirm deploy inputs in Compose and CI; do not infer remote runtime state
   from repository files.

### Trace NellDB without guessing

Treat `internal/store` as the application contract and `internal/nell` as one
adapter. Do not transfer SQLite behavior to NellDB by analogy.

```sh
go doc github.com/samcharles93/NellDB.Store
go doc github.com/samcharles93/NellDB/sdk.DocDB
gopls implementation internal/store/interface.go:16:6
rg -n 'sdk\.New|\.Get\(|\.Put\(|AllDocs|taskKey|docToTask|taskFields' internal/nell
```

Trace `store.Task` field → `sdk.Doc` key → read/conversion → locking or MVCC
around update/transition → reopen/recovery test. The adapter currently uses
`tasks` and `events` collections and performs scans for several queries; verify
those facts in `internal/nell/adapter.go` before relying on them. As of
2026-07-28, `Adapter.Transition` neither locks nor checks its `from` argument;
record that as observed current behavior, not a target promise. NellDB's
`_id`/`_rev`, HLC, and conflict semantics come from the dependency API, while
Archie's legal task transitions remain an Archie concern. Route deeper domain
and persistence background to `archie-nelldb`.

## Reject false confidence

| Weak evidence | Why it fails | Required correction |
|---|---|---|
| One `rg` result | Misses interface dispatch, aliases, generated registration, reflection, and string-addressed protocols | Use gopls, AST candidates, package edges, and composition |
| Green unit test | May construct a component directly and bypass actual config, lifecycle, or transport | Add or inspect composition/contract/integration evidence |
| Fake client and fake server agree | Both sides can mirror the same wrong framing, subject, or state assumption | Compare an independent spec/implementation or real boundary fixture |
| High coverage | Does not establish ownership, maintainability, deprecation, or correct mode selection | Trace authority and current production selection |
| Constructor exists | Does not prove callers use it | Find struct literals and type-aware references |
| Config field decodes | Does not prove the executable reads it | Complete the config-to-observable chain |
| Target PRD says it | Describes required destination, not necessarily current code | Cite current implementation and label the gap |

## Produce the discovery handoff

End with these sections:

1. **Behavior trace:** one row per traversal step with `path:line`, symbol, and
   current responsibility.
2. **Mode matrix:** mode/config branch → concrete implementation → state store
   → transport → consumer.
3. **State and authority:** owner, copies, persistence, transition rules, and
   recovery.
4. **Blast radius:** callers, consumers, tests, config, deploy wiring, docs, and
   compatibility paths.
5. **Duplication/deprecation ledger:** each candidate, evidence it is live or
   dead, replacement, and objective removal gate.
6. **Current-to-target gap:** current fact, target PRD, migration implication;
   never blend the two.
7. **Unknowns:** exact command attempted, output/blocker, and next evidence
   needed.

Do not propose implementation until the trace can answer:

- What entrypoint selects this path?
- Which component owns the state and invariant?
- Which parallel or older path would the feature replace or deprecate?
- Which production modes and persisted data must remain compatible?
- Which evidence would falsify the current understanding?

Short successes are acceptable, but complete one vertical trace rather than
collecting many shallow search hits.

## Provenance and maintenance

Repository evidence verified on 2026-07-28:
`cmd/archied/main.go`, `cmd/archie-agent/`, `internal/{config,daemon,nell,store,workflow,tools/mcp}/`,
`Taskfile.yml`, `.golangci.yml`, `go.mod`, `tools/go.mod`,
`docker-compose.yml`, `.gitea/workflows/deploy.yml`, `CLAUDE.md`,
`ARCHITECTURE.md`, and `docs/prds/`.

Re-verify tools: `go version && gopls version && golangci-lint version && staticcheck -version && go tool`

Re-verify entrypoints: `rg -n '^func main|^func run' cmd/archied cmd/archie-agent`

Re-verify config selection: `rg -n 'config\.(Load|LoadOverlay|LoadDir)\(' cmd internal --glob '*.go'`

Re-verify modules: `find . \( -path './.git' -o -path './.references' -o -path './.claude/worktrees' -o -name node_modules \) -prune -o -name go.mod -print`

Re-verify production construction: `rg -n '&daemon\.Daemon\{|nell\.OpenStore|registerTaskRPCServers|startGateways' cmd/archied/main.go`

Re-verify contract tests: `find internal -type f \( -name '*contract_test.go' -o -name '*architecture_test.go' \) -print | sort`
