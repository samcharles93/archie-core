# Progressive tool disclosure (bridge tools) — wiring

**Status:** Implemented (wiring landed)
**Date:** 2026-08-15
**Compound:** the "keep the LLM tool schema small, give full access on demand" work (issue #167)

## Decision

The bridge-tool pattern is **already implemented and tested** in
`internal/tools/disclosure.go` (`tool_search`, `tool_describe`, `tool_call`
+ `ContextPressureGate` + `DisclosureMode`, section 17.14/17.15) — but it is
**never wired into the daemon**. `BridgeTools` and `ContextPressureGate` are
referenced only in `disclosure_test.go`. This task is therefore a **wiring**
task, not a feature build.

Wire the existing bridge into the per-chat-turn toolset:

1. **Compose a per-turn registry** seeded from the base registry plus the
   per-turn extra tools, register the bridge tools into it, and hand it to both
   the bridge handlers and the `ContextPressureGate`.
2. **Run the gate in `chatGenerateOptions`** (`internal/app/archied/main.go:69`)
   so it decides full-vs-bridge disclosure per turn, and build the
   `core.ToolSet` from the gate's filtered output.
3. **Thread the model's context window** into the preparation seam so the gate
   has a real `ContextWindowSize` (it disables itself at 0, falling back to
   full disclosure).

## Why the naive "register extras into the base registry" is wrong

`BuildToolSetFrom` (`internal/agentexec/toolset.go:170`) documents the invariant:

> the task tools carry the calling gateway's identity, so there is one set of
> them per gateway and they **cannot live in the process-wide registry**.

This is confirmed by wiring: gateways are per-channel
(`telegram_setup.go:291,308` build a `TurnRunner` per channel), and the extra
tools (`task_*`, `session_*`, `dashboard_*`) close over that channel's identity
via `TurnRunnerConfig` (`turn.go:66`). Registering them into `b.toolReg` would
let channel A's session/identity-bound tools be invoked by channel B — a real
cross-channel privilege bug. The bridge handlers iterate `reg.All()`
(`disclosure.go:53,107,146`), so the extras must be discoverable; the fix is a
**per-turn composed registry**, not a mutation of the process-wide one.

## Shape of the change

### 1. Per-turn composed registry + gate inside `chatGenerateOptions`

Replace the current base-set + extra-set merge (`main.go:90-100`) with a single
composed-registry pass:

```go
// new helper, in internal/tools/disclosure.go or the archied wiring package
func ComposeForTurn(reg *tools.Registry, extra []tools.ToolEntry) (*tools.Registry, error) {
    composed := tools.NewRegistry()
    for _, e := range reg.All() {
        if err := composed.Register(e); err != nil { return nil, err }
    }
    for _, e := range extra {
        if err := composed.Register(e); err != nil { return nil, err }
    }
    if _, err := composed.RegisterBatch(tools.BridgeTools(composed)); err != nil {
        return nil, err
    }
    return composed, nil
}
```

`chatGenerateOptions` then:

```go
composed, err := ComposeForTurn(registry, extra)
// ...
gate := tools.NewContextPressureGate(contextWindow)
gate.SetAlwaysVisible(...)        // e.g. session/session_resume, task/task_list
disclosed := gate.FilterTools(composed)
toolSet, err := agentexec.BuildToolSetFrom(disclosed, toolOpts)
```

`FilterTools` (`disclosure.go:327`) already returns `all` in full mode and
`bridge + always-visible` in bridge mode, and `BuildToolSetFrom` re-applies
availability/approval/schema rules, so the two layers compose cleanly.

### 2. Thread context window through the preparation seam

`ContextPressureGate` disables at `ContextWindowSize == 0`. The model context
window is resolved in `TurnRunner.prepareTurn` (`turn.go:303-306`) but is not
passed to `TurnModel.Prepare`. Extend the seam:

```go
type TurnPrepareContext struct {
    Model         string
    Extra         []tools.ToolEntry
    ContextWindow int      // model's context window in tokens; 0 = gate disabled
}
```

`TurnModel.Prepare(ctx, context TurnPrepareContext)`; `prepareTurn` fills
`ContextWindow` from `modelDetails.ContextWindow`. This touches one interface
(`turn.go:18`), one production impl (`chat_turn_model.go:40`), and the test
mock (`turn_test.go:21`).

### 3. Bridge-handler parity note

`bridgeCallHandler` calls `e.Handler` directly (`disclosure.go:158`), bypassing
`toolExecute` — so a `tool_call` invocation does **not** apply `CapPayload` /
`MaxResultSizeChars` spilling or `OnToolCall` reporting, and it pre-refuses
`RequiresApproval` tools (`disclosure.go:150-152`). This is acceptable for the
first wiring (the bridge is read-discover/describe plus a guarded call), but a
later hardening should route the invoked tool through the same
cap/approval path the model's direct call uses. Recorded as a follow-up, not a
blocker for this task.

## Files touched

- `internal/app/archied/main.go` — `chatGenerateOptions` composes + gates.
- `internal/app/archied/chat_turn_model.go` — `Prepare` accepts `TurnPrepareContext`.
- `internal/gateway/turn.go` — `TurnModel.Prepare` signature; `prepareTurn` fills context window.
- `internal/gateway/turn_test.go` — update the `turnTestModel.Prepare` mock.
- `internal/tools/disclosure.go` — add `ComposeForTurn` helper (or place in archied).

## Out of scope / follow-ups

- Delegate `tool_call` through `toolExecute` for cap/approval/observability parity.
- Cover the compose-and-gate path with tests (compressed footprint under a
  small window; always-visible minus bridge margin).
- Reconcile any `ContextPressureGate` mode with the compression budget already
  computed in `prepareTurn` (`turn.go:311-315`).
