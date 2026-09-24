---
name: archie-codebase-discovery
description: "Trace Archie behaviour and blast radius through Go source, types, state, persistence, transports, consumers, tests, configuration, production composition, and target architecture. Load before changing or planning a non-trivial feature, refactor, config field, interface, persistence path, workflow, channel, plugin, or agent execution path; also load to investigate duplicate behaviour, hidden dead code, constructor bypasses, package dependencies, decoded-but-unwired configuration, or uncertainty about what a symbol actually affects."
---

# Discover the Archie codebase

Use evidence from the current checkout to explain a behaviour before changing it.
Volatile tool facts below were verified on **2026-09-18**.

**Composition roots**: `cmd/archied/main.go` (thin dispatcher) →
`internal/app/archied.Run()`, and `internal/app/agentworker` (invoked by
`cmd/archie-agent/main.go`). Do not treat `cmd/*` as the wiring site.
**Production wiring**: a path selected by a shipped entrypoint — not a
constructor, decoder, or passing unit test. **State owner**: component with
authority to validate and persist a state transition.

## Establish a reproducible baseline

```sh
go version; go env GOMOD GOWORK GOOS GOARCH
gopls version; golangci-lint version; staticcheck -version
```

As of 2026-09-18: Go 1.27.0, gopls 0.23.0, golangci-lint 2.13.2,
staticcheck 2026.2.1. Use `gopls call_hierarchy` (no `go tool callgraph`;
`go tool` lists only asm, cgo, compile, cover, fix, link, preprofile, vet).

Writable caches when defaults are read-only:

```sh
export GOCACHE="${TMPDIR:-/tmp}/archie-discovery-gocache"
export GOLANGCI_LINT_CACHE="${TMPDIR:-/tmp}/archie-discovery-lint"
export GOTMPDIR="${TMPDIR:-/tmp}"
export GOPROXY=off GOFLAGS=-mod=readonly
```

Module boundaries (root and `tools/` are separate):

```sh
find . -name go.mod -not -path './.git/*' -not -path './node_modules/*'
go list -m -f '{{.Path}} {{.GoVersion}}'
go -C tools list -mod=readonly -m -f '{{.Path}} {{.GoVersion}}'
```

## Build the behaviour trace

Write: behaviour, observable input, expected output, state that may change, modes.

| Step | Find                | Required evidence                                                        |
| ---: | ------------------- | ------------------------------------------------------------------------ |
|    1 | Entrypoint          | `main`, handler, poller, RPC subject, or tool registration               |
|    2 | Construction        | Constructor and every direct struct literal                              |
|    3 | Contract            | Interface, wire type, command, event, or public method crossed           |
|    4 | Implementation      | All type-valid implementations, selected per mode                        |
|    5 | State owner         | Function validating transition, owning concurrency/transaction rules     |
|    6 | Persistence         | Store adapter, schema/key/document, serialisation, reload/recovery       |
|    7 | Event/transport     | Publish/send and subscribe/receive, subject/framing, timeout, retry, ack |
|    8 | Consumer            | Callers and downstream readers                                           |
|    9 | Tests               | Unit, integration, contract, architecture, production-composition        |
|   10 | Configuration       | Decode, default, validation, normalisation, composition-root consumption |
|   11 | Target architecture | Current-to-target mapping in `docs/prds/`; label gaps                    |

Start at these verified anchors:

| Concern                     | Current anchor                                                                                                            |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| Resident daemon             | `cmd/archied/main.go` → `internal/app/archied.Run()` (`internal/app/archied/main.go`) → `daemon.Daemon`                   |
| Sandboxed worker            | `cmd/archie-agent/main.go` → `internal/app/agentworker/`; NATS boundary in `internal/infrastructure/agenttransport/nats/` |
| Task orchestration          | `internal/daemon/daemon.go`, `internal/domain/workflow/`                                                                  |
| Task contracts              | `internal/domain/storecontract/storecontract.go` (`TaskStore`)                                                            |
| Production task persistence | `internal/infrastructure/postgres/`; built in `internal/app/archied/state_store.go`                                       |
| RPC split                   | `internal/{taskrun,natsrpc,forgerpc,worktreerpc}/` plus `internal/infrastructure/staterpc/` (gRPC State Store)            |
| Chat and channels           | `internal/gateway/`, `internal/channels/`                                                                                 |
| Configuration               | `internal/config/`, `internal/infrastructure/configuration/`, `config.example.toml`, `deployments/*.toml`                 |
| Target map                  | `docs/prds/01-project-management.md` and linked `docs/architecture/*.md`                                                  |

## Use syntax, type, and call evidence

### Map packages before reading files

```sh
go list -f '{{.ImportPath}}|go={{join .GoFiles ","}}|tests={{join .TestGoFiles ","}}' ./internal/infrastructure/postgres
go list -json ./internal/infrastructure/postgres
.agents/skills/archie-codebase-discovery/scripts/package-edges.sh ./internal/...
```

### Resolve symbols with gopls

1-indexed `file:line:column`:

```sh
gopls symbols internal/domain/storecontract/storecontract.go
gopls definition cmd/archied/main.go:27:14
gopls references -d internal/domain/storecontract/storecontract.go:33:2
gopls implementation internal/domain/storecontract/storecontract.go:33:2
gopls call_hierarchy internal/domain/storecontract/storecontract.go:33:2
go doc ./internal/domain/storecontract.TaskStore
go test ./internal/infrastructure/postgres -list '.'
```

### Generate AST candidates for construction

```sh
.agents/skills/archie-codebase-discovery/scripts/scan-go-type-syntax.sh Daemon .
.agents/skills/archie-codebase-discovery/scripts/scan-go-type-syntax.sh -tests Task .
```

Confirms candidates with `gopls definition` or `gopls references`. Pass
`-tests` to expose test-only construction.

## Run focused discovery recipes

### Find duplicate behaviour paths

```sh
golangci-lint run --enable-only=dupl ./internal/...
rg -n 'Transition\(|PublishTask\(|ClaimNext\(|StatusWaitingHuman' cmd internal
```

Trace each candidate to entrypoint and state owner. `dupl` finds similar syntax,
not two different implementations of one responsibility.

### Find unused-but-compiled code

```sh
staticcheck -checks=U1000 ./internal/...
```

For every suspect: `gopls references -d`; search init, reflection/Yaegi, tags,
RPC subjects, plugin manifests, string dispatch; check build tags/OS files;
search both composition roots. `U1000` does not prove exported/reflection/wire
targets are dead.

### Find interface implementations

```sh
gopls implementation
rg -n 'var _ .*=' --glob '*.go' cmd internal
```

Trace which implementation the composition root selects for each mode.

### Find struct-field consumers

`gopls references -d` on field declaration, not common name. Trace assignments
and reads separately. Follow `ForTask`/`ToConfig` copies. Check JSON/TOML/YAML
tags. Compare production and `_test.go` references.

### Detect constructor bypasses

Run `scan-go-type-syntax.sh Type .`. Inspect struct literals outside owning
package. Compare validation/defaulting in constructors with direct literals.
Use `gopls references` on constructor and type.

### Prove package dependency direction

```sh
.agents/skills/archie-codebase-discovery/scripts/package-edges.sh ./internal/...
go list -deps -json <pattern>
```

### Find decoded-but-unused configuration

Prove: `file/key → decoder → default → validation → normalisation/copy →
composition-root read → concrete component → observable behaviour`.

```sh
rg -n 'toml:"|yaml:"|json:"' internal/config
rg -n 'Resolve|Overlay|Dir|finalize|ForTask|ToConfig' internal/infrastructure/configuration internal/app/archied
gopls references -d <exact-field>
```

Decoding lives in `internal/infrastructure/configuration`, not `internal/config`.
`configuration.New(log)` returns a `Loader`; the composition root in
`internal/app/archied/bootstrap.go` calls `Loader.Resolve` (with `Loader.Overlay`
and `Loader.Dir` for overlay/`conf.d` inputs). There is no `config.Load` or
`config.LoadDir`.

### Prove production composition

1. Start at `internal/app/archied/main.go` (`Run`), entered from
   `cmd/archied/main.go`.
2. Record config condition per branch.
3. Follow concrete construction into `daemon.Daemon` and gateway start.
4. For container/NATS behaviour, trace `cmd/archie-agent/main.go` into
   `internal/app/agentworker` and `internal/infrastructure/agenttransport/nats`.
5. Trace both sides of every RPC/transport and error/timeout paths.
6. Read contract/architecture tests.
7. Confirm deploy inputs in Compose and CI.

### Trace persistence

Treat `internal/domain/storecontract` as the application contract and
`internal/infrastructure/postgres` as the production adapter behind it; the
schema is `internal/infrastructure/postgres/migrations/` and the queries are
sqlc-generated from `queries/`.

```sh
gopls implementation internal/domain/storecontract/storecontract.go:33:2
rg -n 'CREATE TABLE|CREATE INDEX' internal/infrastructure/postgres/migrations
```

## Reject false confidence

| Weak evidence                | Why it fails                                                           | Required correction                                   |
| ---------------------------- | ---------------------------------------------------------------------- | ----------------------------------------------------- |
| One `rg` result              | Misses interface dispatch, aliases, generated registration, reflection | Use gopls, AST candidates, package edges, composition |
| Green unit test              | May bypass config, lifecycle, transport                                | Add composition/contract/integration evidence         |
| Fake client and server agree | Both mirror same wrong framing/subject/state assumption                | Compare independent spec or real boundary fixture     |
| High coverage                | Does not establish ownership, maintainability, mode selection          | Trace authority and production selection              |
| Constructor exists           | Does not prove callers use it                                          | Find struct literals and type-aware references        |
| Config field decodes         | Does not prove executable reads it                                     | Complete config-to-observable chain                   |
| Target PRD says it           | Not necessarily current code                                           | Cite current implementation and label gap             |

## Produce the discovery handoff

1. **Behaviour trace:** path:line, symbol, current responsibility per traversal step.
2. **Mode matrix:** mode/config branch → concrete implementation → state store
   → transport → consumer.
3. **State and authority:** owner, copies, persistence, transition rules, recovery.
4. **Blast radius:** callers, consumers, tests, config, deploy wiring, docs,
   compatibility paths.
5. **Duplication/deprecation ledger:** each candidate, live/dead evidence,
   replacement, objective removal gate.
6. **Current-to-target gap:** current fact, target PRD, migration implication.
7. **Unknowns:** exact command, output/blocker, next evidence needed.

Do not propose implementation until the trace answers: what entrypoint selects
this path, which component owns state, which parallel/older path the feature
replaces, which production modes and persisted data must remain compatible,
which evidence would falsify current understanding.
