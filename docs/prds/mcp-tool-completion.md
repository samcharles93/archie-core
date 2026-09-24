# MCP client and tool-guardrail completion

**Status:** Approved

Epic: `archie-core-1786637493439-140-2cee2e9b` / GitHub `#161` ("[EPIC] Tools:
central registry, MCP client, guardrails").

This document is scoped to five sub-features of that epic: MCP
server-initiated sampling, the per-server parallel-tool-calls flag, sandboxed
MCP tool servers, a Firecracker sandbox backend, and sequential dispatch with
per-result previews.

Out of scope, and not to be touched: the central registry
(`internal/tools/registry.go`), all three MCP transports (stdio/HTTP/SSE,
`internal/tools/mcp/transport*.go`), reconnect-with-backoff
(`transport.go`), guardrail loop detection
(`internal/tools/guardrail.go`), idempotent/mutating classification
(`internal/tools/classification.go`), concurrent tool dispatch (`#183`), and
progressive disclosure wiring (`internal/app/archied`). These are settled and
production-wired.

## Problem

`bd search "MCP client"` shows five closed transport beads and one open:
server-initiated sampling (`#151`). `internal/tools/mcp/client.go`
holds a single `callMu *sync.Mutex` that serialises every `tools/call`
regardless of server — `ParallelToolCalls` does not exist on the `MCPServer`
config struct (`internal/config/config.go`), confirming `#177` is a
real gap, not a stale bead. `#178` (wire MCP tool servers for the chat agent,
sandboxed) and `#179` (Firecracker substrate) have no corresponding code —
MCP servers run as whatever the config's `Command`/`Args` launches,
unsandboxed. `#182` (sequential tool dispatch with per-result previews) is
open; concurrent dispatch (`#183`) is closed, so there are two dispatch paths
and this PRD decides how they coexist.

## Design

Sub-features are independent; there is no new shared type this document
introduces. Each section below stands alone.

## MCP server-initiated sampling (`#151`)

**Decision.** Implement as a callback on `mcp.Client`, not a second
transport. The MCP spec's `sampling/createMessage` request flows server→client
over the same connection each transport already maintains
(`transport.go`'s stdio/HTTP/SSE all multiplex request and response frames).
Add a `SamplingHandler func(context.Context, SamplingRequest) (SamplingResult, error)`
field to the client's config struct next to the existing transport fields,
defaulting to nil (server sampling requests get a JSON-RPC "not supported"
error when unset — never a silent drop).

**Call site.** `internal/tools/mcp/client.go` — add the handler field and a
dispatch branch in whatever function demuxes incoming server
messages by method name (the same switch that already routes
`notifications/*`). Wire a concrete handler in
`internal/app/archied/main.go` that routes the sampling request through the
same chat-model call path `chatGenerateOptions` already uses, so a
server-initiated sample gets the daemon's configured model, not a second
provider client.

**Reuse check.** The chat model invocation path already exists
(`chatGenerateOptions`, `main.go`); this reuses it rather than adding a
parallel model-calling path for MCP sampling.

**Failure mapping.**

| condition                       | contract error                                                                             |
| ------------------------------- | ------------------------------------------------------------------------------------------ |
| no `SamplingHandler` configured | JSON-RPC method-not-found, per spec                                                        |
| handler returns error           | JSON-RPC internal error, message from handler                                              |
| handler timeout                 | JSON-RPC timeout error; use the transport's existing request-timeout config, not a new one |

**Testing.** Fakeable entirely — a stub `SamplingHandler` and a fake
transport that emits a `sampling/createMessage` frame. No real backend
needed; belongs in `task check`.

## Per-server parallel-tool-calls flag (`#177`)

**Decision.** `ParallelToolCalls bool` (default `false`) goes on `MCPServer`
in `internal/config/config.go`, next to `Transport`/`Command`. The
mutex in `client.go` becomes per-server: when `ParallelToolCalls` is
false (default, and the only behaviour), keep the single `callMu`
serialising that server's calls exactly as now; when true, drop the mutex for
that server's `Client` instance entirely and let the caller's own
concurrency (already proven by the closed concurrent-dispatch bead, `#183`)
handle it. This is additive — no server's behaviour changes unless an operator
opts in.

**Call site.** `internal/config/config.go` (struct field),
`internal/tools/mcp/client.go` (make the mutex conditional on the
field), and wherever `MCPServer` configs are turned into `Client` instances
at daemon startup (the constructor that reads `internal/config`'s MCP block).
`config.example.toml`'s commented MCP block gets the new field documented
with its default.

**Testing.** Unit test asserting two concurrent `Call`s to the same
`Client` block on each other when the flag is false and don't when true —
fakeable with a slow fake transport. `task check`.

## Sandboxed MCP tool servers for the chat agent (`#178`) and Firecracker substrate (`#179`)

**Decision.** These two beads are one design decision, not two: `#179` is an
alternate execution backend for `#178`, not separate scope. Sandbox MCP
servers that a chat-turn config marks as untrusted (a new
`Sandboxed bool` field on `MCPServer`, default `false` — existing
configured servers are unaffected) behind the same `ContainerPool` the
daemon already uses to run agent work
(`internal/container/pool.go`, per CLAUDE.md's `WriteTaskJSON` trap and the
`Acquire`/`Release` lifecycle `daemon.go`'s `acquireTaskContainer` already
proves). Do not build a second sandbox mechanism: an MCP stdio server is a
subprocess exactly like an agent task container's entrypoint, so launching it
via `ContainerPool.Acquire` with stdio piped to the `mcp.Client` transport is
the smallest change, not new plumbing.

Firecracker is **not** built now. `ContainerPool`'s existing Docker-backed
isolation is the shipped backend; Firecracker becomes a second
implementation of the same acquire/release contract only if a concrete
escape or resource-isolation gap is measured against Docker-backed sandboxing
in production. Recording this as the deletion/promotion gate keeps `#179`
honestly `OPEN` instead of speculatively building an unused backend, per
CLAUDE.md's Architectural Simplicity rule.

**Call site.** `internal/config/config.go` (`MCPServer.Sandboxed`), the MCP
client constructor (route sandboxed servers' process launch through
`container.Pool.Acquire` instead of `os/exec` directly), and
`internal/container/pool.go` (confirm its `Acquire` contract accepts a
stdio-piped, non-worktree workload — if it assumes a git worktree
per CLAUDE.md's `WriteTaskJSON` note, add the narrower entrypoint variant
there rather than duplicating the pool).

**Error/failure mapping.**

| condition                          | contract error                                                                                                                        |
| ---------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| pool exhausted (no container slot) | MCP client returns connection-refused equivalent; server marked unavailable in the registry until retried                             |
| container fails health/start       | same as `#151`'s handler-error path — surfaced through the client's existing reconnect/backoff (`transport.go`), not a new retry loop |

**Testing.** `Sandboxed=false` path is the existing, already-tested
unsandboxed flow — no change. `Sandboxed=true` needs a real
`ContainerPool` (Docker) — manual smoke command, not `task check`; document
the exact command in the runbook this PRD's issue links to once implemented.

## Sequential tool dispatch with per-result previews (`#182`)

**Decision.** This coexists with concurrent dispatch (`#183`, closed) as a
second, explicit mode selected by the caller — not a replacement. Concurrent
dispatch is for independent tool calls in one turn; sequential-with-preview
is for a turn where each result should be surfaced to the operator (or the
next tool call) before the next call fires — e.g. a mutating tool chain where
showing intermediate state matters. Add a `DispatchMode` enum
(`Concurrent`/`Sequential`) to whatever type selects the closed
`#183` concurrent path, defaulting to `Concurrent` (its only behaviour).

**Call site.** The tool-dispatch entry point that `#183`'s closed
implementation lives in (grep the concurrent-dispatch symbol added for
`#183` — same file gets the sequential branch, not a parallel file). Preview
surfacing reuses whatever channel-adapter interface already streams
intermediate turn output (the same seam `internal/channels/telegram/` and
other adapters use for streaming deltas) — do not invent a second preview
transport.

**Testing.** Fakeable with a fake tool that returns staged results; assert
preview callbacks fire in order before the next call starts. `task check`.

## Call site inventory

| concern                                             | file                                                                               | change                                       |
| --------------------------------------------------- | ---------------------------------------------------------------------------------- | -------------------------------------------- |
| MCP client transports (stdio/HTTP/SSE)              | `internal/tools/mcp/transport*.go`                                                 | none — already handles this                  |
| MCP client call serialisation                       | `internal/tools/mcp/client.go`                                                     | change (`#177`)                              |
| MCP server config                                   | `internal/config/config.go`                                                        | new fields: `ParallelToolCalls`, `Sandboxed` |
| Sampling dispatch                                   | `internal/tools/mcp/client.go`                                                     | new (`#151`)                                 |
| Sampling handler wiring                             | `internal/app/archied/main.go`                                                     | new (`#151`), reuses `chatGenerateOptions`   |
| Sandboxed MCP launch                                | MCP client constructor + `internal/container/pool.go`                              | new (`#178`/`#179`)                          |
| Concurrent tool dispatch                            | dispatch entry point (closed `#183`)                                               | none — already handles this                  |
| Sequential tool dispatch + previews                 | same dispatch entry point                                                          | new (`#182`)                                 |
| Guardrails / classification / registry / disclosure | `internal/tools/guardrail.go`, `classification.go`, `registry.go`, `disclosure.go` | none — already handles this                  |
| `config.example.toml` MCP block                     | `config.example.toml`                                                              | doc update for new fields                    |

## Execution: multi-agent team breakdown

| sub-feature                          | issue          | implementer scope                                                                  | suggested council lenses            | why                                                                                             |
| ------------------------------------ | -------------- | ---------------------------------------------------------------------------------- | ----------------------------------- | ----------------------------------------------------------------------------------------------- |
| Server-initiated sampling            | `#151`         | `client.go` dispatch branch, `main.go` handler wiring                              | `lens-contract`                     | new JSON-RPC method surface crossing the MCP wire contract                                      |
| Parallel-tool-calls flag             | `#177`         | config field, conditional mutex, constructor wiring                                | `lens-boundary`                     | per-server behaviour change must not leak into other servers' serialisation                     |
| Sandboxed MCP + Firecracker deferral | `#178`, `#179` | config field, constructor routing through `ContainerPool`, pool entrypoint variant | `lens-operator`, `lens-deletionist` | operator: sandbox-down failure path; deletionist: keeps `#179` from becoming unused scaffolding |
| Sequential dispatch with previews    | `#182`         | `DispatchMode` enum, sequential branch, preview reuse of streaming seam            | `lens-maintainer`                   | two coexisting dispatch modes need a reader to tell which applies without re-deriving both      |

## File and link the beads

- `#151`, `#177`, `#178`, `#179`, `#182` each get their description updated to
  point at this doc's matching `##` heading instead of restating the design.
- Close `#179` as a design decision (link here) rather than leaving it open
  as separate scope — it is not built now, and the doc records why.
- Add the suggested lenses to each issue directly.
