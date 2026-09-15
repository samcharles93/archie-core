# Contract conformance audit — decision

**Status:** Decided, implementing
**Date:** 2026-09-15
**Authority:** sibling to `archie-core-06ag` (standing reachability audit) and
`docs/architecture/dependencies-and-contracts.md`. Does not replace either.
**Extends:** `docs/architecture/generated-documentation.md` (the `docsgen check`
gap it admits at line 331), `docs/prds/config-example-drift.md` (step 3).

## Question

Archie has strong contract enforcement at exactly one boundary and none at the
rest. `proto/*.proto` is generated, linted against `main`, and regenerated in
`task check`; `proto:check` fails on any drift. The **application** boundary —
where the dashboard, the generated documentation, and the shipped asset bundle
meet their consumers — has no equivalent, and three separate documents already
admit drift there in writing.

Should contract conformance be a standing, runnable audit like `reachaudit`?

## Decision

**Yes, and it is a sibling of reachability, not a replacement.** Reachability asks
*"is this code in a shipped binary?"*. Conformance asks the complementary
question: *"does every declared contract surface have a consumer, or an explicit
declaration that it has none?"* A capability can be reachable and unconsumed —
`ratelimit` was in the binary closure and called by nothing
(`archie-core-6wxb`). Reachability would not have found that; this would.

The audit reports three classifications, and **the third is the only finding**:

| Classification | Meaning | Action |
| --- | --- | --- |
| `CONSUMED` | a non-test client call site exercises it | none |
| `DECLARED-UNCONSUMED` | named in the declaration file with a reason and a tracker id | none — it is tracked work |
| `UNDECLARED-UNCONSUMED` | nobody reaches it and nobody declared it | **the finding** |

`DECLARED-UNCONSUMED` is a status, not a defect — the same posture `reachaudit`
takes with `unreachable-tracked`. The declaration file is an allowlist that
**must match reality exactly**: an entry naming a surface that is now consumed is
itself a failure (stale declaration), because a stale allowlist is how a gate
quietly stops checking anything.

### Surfaces, and what proves each is consumed

| Surface | Contract of record | Consumed means |
| --- | --- | --- |
| `proto/state/v1` `StateStoreService` (43 RPCs) | the `.proto` | an exported method on `staterpc.Client` whose body calls that RPC |
| `proto/gateway/v1` `ChatService` (12 RPCs) | the `.proto` | an exported method on `gatewayrpc.Client` calling that RPC |
| `internal/webui` HTTP routes (52) | the `registerX` call sites | a path literal in `ui/src/base/api.jsx` or an `EventSource` subscriber |
| `docs/data/generated/contracts.json` | `tools/docsgen` | a committed-artifact comparison (`docsgen check`) |
| `ui/dist` | `ui/src` | a rebuild that produces no diff |

RPC coverage is derived by **reflection over the exported client type**, not by
parsing call sites. If a method exists, the generated `grpc` interface already
forced it to make a real RPC call, so presence of the method *is* the proof of
consumption. This keeps the check exact without a call-graph.

## The two gaps this closes immediately

Both are already admitted in writing; neither is implemented.

**1. `docsgen check` is documented and does not exist.**
`generated-documentation.md:118-121` advertises `docsgen data|asyncapi|all|check`
and line 336 gives the invocation `go -C tools run ./docsgen check --repo-root ..`.
`tools/docsgen/main.go` defines exactly two flags, `--repo-root` and `--out`, and
`run()` unconditionally writes. Line 331 admits "`task check` does not run
`docs:check`, so generated drift is currently ungated" — and it cannot, because
there is no `check` mode and no `docs:*` task to run.

**2. The dashboard's client is not centralised, and its own comments say it is.**
`ui/src/base/api.jsx` opens with "Single place that knows how to talk to archied.
Every feature folder goes through this, so auth handling and error shape live in
one file." **22 of its 39 calls use a raw `fetch()` that bypasses its own `req()`
wrapper**, and `ui/src/chat/chat.jsx:406` calls `fetch("/api/chat/stream")` from
outside `api.jsx` entirely. The consequence is concrete, not stylistic: `req()`
is the only place that maps `401` to `ApiError("unauthorised", 401)`, which
`classifyActionError` turns into `session-expired`. Every raw-`fetch` path
therefore reports an expired session as a generic `refused`/`broken` failure —
which is the exact defect `archie-core-1786637490536-29-f1dab2c4` ("Make dashboard
authentication and connectivity failures actionable") exists to fix.

This is the check the audit's own harness must survive: it asserts the
centralisation claim, and today it fails.

## Shape

```text
tools/contractaudit/
  main.go         # flag parsing, report rendering (advisory by default)
  surface.go      # Surface interface: Name() + Findings() []Finding
  proto.go        # RPC coverage by reflection over the exported client types
  webui.go        # route registration vs ui/src call sites
  generated.go    # docsgen check-mode comparison
  declarations.go # the allowlist, with exact-match staleness
  testdata/       # negative-test fixtures
docs/contract-declarations.json   # DECLARED-UNCONSUMED allowlist
```

Finding shape, stable because tests and the report both consume it:

```go
type Finding struct {
    Surface    string // "proto/state/v1"
    Subject    string // "StateStoreService/TokensByDay"
    Class      string // CONSUMED | DECLARED-UNCONSUMED | UNDECLARED-UNCONSUMED
    Evidence   string // file:line, or the reason string
    Tracker    string // bead id for DECLARED entries, empty otherwise
}
```

Advisory is the default: findings print, exit status is 0. `--strict` (and
`task contract` in the gate, once the initial findings are triaged) fails on any
`UNDECLARED-UNCONSUMED` or stale declaration.

## Blind spots, labelled rather than omitted

Following `reachaudit`'s rule that Yaegi/reflection packages are "labelled, never
presented as dead":

- **Runtime-loaded code** (Yaegi, reflection) — a route or RPC reached only
  through dynamic dispatch reads as unconsumed. Labelled with the package list.
- **Dashboard call-site extraction is textual, and deliberately narrow.** A path
  counts as a consumer only when it is the *first argument of a call* in code:
  the function need not be named (so renaming `req(` to `request(` is fine), but
  a bare constant, a dead array, an object property, or a commented-out call
  must not count. Both halves are load-bearing, and they pull in opposite
  directions.

  Naming the call broke on rename: matching `req|fetch` reported 16 live routes
  as unconsumed the moment the dashboard's helper was renamed. Dropping the
  call requirement was **worse**, and is recorded here because it is the more
  tempting mistake: crediting any quoted `/api/...` literal meant a comment, a
  retired-route array, or a display label proved a route was reachable, so
  deleting every real request still read as healthy. The two directions are not
  symmetric — a false "unconsumed" is a loud, fixable failure, while a false
  "consumed" is a silent hole, which is worse than having no gate.

  Remaining limits: a path passed as any argument other than the first is not
  seen (accepting a comma would start crediting array elements and restore the
  vacuity above); a request written inside a template-literal interpolation is
  not seen; and a path built from a bare `/api` fragment is reported as
  unparsable rather than silently contributing nothing.
- **Routes are credited per path, not per verb.** A literal does not say which
  HTTP verb carries it, so a route is credited to every verb and a route whose
  specific verb has no caller reads as consumed.
- **An `EventSource` subscription is a consumer.** `/events` and
  `/api/logs/stream` are consumed via `EventSource`, not `fetch`; an extractor
  that only looks for `fetch` reports them unconsumed and is simply wrong.

## Acceptance

1. `go -C tools run ./contractaudit` runs on demand and prints a report; exit 0.
2. Every unconsumed surface appears with a classification and, where declared, a
   reason and tracker id.
3. The declaration file fails on a **new** undeclared entry *and* on a **stale**
   entry that is no longer true.
4. Blind spots are labelled in the report, never silently counted as findings.
5. Findings are advisory; nothing is deleted or rewritten by this tool.
6. `docsgen check` exists and `task check` runs it, so committed generated data
   cannot drift.
7. **Each check is negative-tested**: a violation is injected, the check is
   confirmed to fail for the right reason, and the injection is reverted. A check
   that has never failed is not a check.

## Non-goals

- No runtime schema validation of HTTP responses. The webui handlers already
  return non-nil slices on every empty path (`[]SkillView{}`, `[]CuratorView{}`,
  `make([]taskView, len(tasks))`), so the nil-slice class does not currently
  apply; adding a live-stack validator before it has a defect to find is
  scaffolding, not a gate. Recorded as deferred, not rejected.
- No re-parse of the proto as a second source of truth. The generated Go types
  are the contract; `buf lint`/`buf breaking` already own the `.proto`.
- No generation of `config.example.toml` or `deployments/*.toml` — settled
  against in `config-example-drift.md`.
- No change to `tools/reachaudit`'s scope or report shape.
