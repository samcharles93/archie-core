# /status health surface -- what's shipped, what's proposed

**Status:** Phase 1 shipped 2026-09-03 (archie-core-wp9s). Phase 2 below is
a **proposed resolution, awaiting Sam's sign-off** -- not yet implemented.

## Background

`archie-core-wp9s` ("/status and /tasks have the wrong division of labour")
asked for two things: `/tasks` owning the live per-task work view, and
`/status` owning daemon health -- "broker connectivity, provider
reachability, channel state, container pool, queue depth, last poll,
version" -- with neither duplicating the other or static config available
elsewhere (`/model` already lists active provider/model).

## Phase 1 -- shipped

- `/tasks`: new command, `gateway.ChatTaskSummary` extended with
  `Stage`/`Attempt`/`ParkReason`/`UpdatedAt` (all already persisted on
  `store.Task`, zero new instrumentation), sorted actionable-first.
- `/status`: itemized per-status task counts replaced with one aggregate
  `Queue: N in flight (...)` line (running/waiting/parked only -- queued and
  terminal states excluded as not a health concern). Runtime
  (provider/model) is temporarily left in place; see the open question
  below.

Not touched: `/agents` (`gateway.AgentReader`/`Router.Agents`) is a separate,
pre-existing command that already has the right shape for "list active
work" but is never wired to a real implementation in any composition-root
call site (`bootstrap.go`, `telegram_setup.go`) -- it always answers "Agent
listing is not configured." in production. That is a standalone bug
(`archie-core-mxls`), not folded into this work: deciding whether to wire
it, repurpose it, or delete it in favor of `/tasks` is Sam's call, not
something to resolve by implication here.

## Phase 2 -- health checks, verified against the actual tree

Each bullet below was checked against real code before being called cheap
or not; nothing here is guessed.

### Cheap -- no design decision needed, straightforward to add

- **Broker connectivity.** `internal/infrastructure/eventbus/nats.Client`
  already exposes `CoreConn() (*nats.Conn, error)`
  (`client.go:134`). The underlying `nats.Conn` (nats.go) has its own
  `Status()`/`IsConnected()`. A `Client.Connected() bool` wrapper is a few
  lines.
- **Container pool.** `container.Pool` already tracks `active int` under
  its own mutex (`pool.go:90`) but has no public accessor. A
  `Pool.Active() int` getter is a few lines.
- **Last poll.** Nothing tracks this today. The daemon's poll loop
  (`internal/daemon`) needs one `lastPollAt time.Time` field (atomic or
  mutex-guarded, matching the pool's own pattern) set at the top of each
  poll tick, with a getter.
- **Queue depth.** Already shipped in Phase 1.

### Not cheap -- needs a decision before coding

- **Provider reachability.** No existing check anywhere. The only way to
  answer "is the provider reachable" is a real network call, which means:
  a `/status` invocation either pays that latency and cost every time
  (bad -- `/status` should be instant, matching `/model`'s and `/tasks`'
  own current behaviour), or `/status` reports the **last** call's
  outcome (success/failure/never-called) rather than probing live. The
  latter needs a small success/failure counter or timestamp recorded at
  the one place LLM calls already go through (`ai-sdk` responder path),
  not a new probe. Recommend: last-known-outcome, not live probing --
  but this is a real product tradeoff (freshness vs. cost) worth Sam's
  explicit sign-off before it's built into a command he uses daily.
- **Channel state.** `channels.Channel` (embeds `gateway.Gateway`:
  `Name()/Start()/Stop()`) has no connectivity/health method today, and
  telegram/email/webhook each manage their own connection lifecycle
  independently. Answering "is Telegram actually connected" needs either
  (a) a new method on the `Channel`/`Gateway` contract every implementation
  must satisfy, or (b) each channel self-reporting into a shared registry
  the daemon already holds (`ChannelManager` -- `bootstrap.go` already
  calls `b.channelManager.MarkStarting/MarkRunning/MarkFailed` per
  channel). (b) is very likely the right shape since `ChannelManager`
  already exists and already tracks exactly this kind of state for the
  dashboard; needs a read method exposed to `/status`. Flagging as
  "needs a decision" only because it touches a contract multiple
  implementations satisfy, not because the shape is unclear -- ChannelManager
  is the obvious answer.
- **Version.** Deliberately **not** proposed for `/status`: `/version`
  already owns this (`Router.Version`, `handleVersion`), and duplicating it
  back into `/status` would reintroduce the exact "answers something
  available elsewhere in chat" complaint this ticket exists to fix.

## Recommendation

Ship the three cheap items (broker connectivity, container pool, last
poll) as a mechanical follow-up alongside Phase 1's `Queue:` line --
same shape, same non-duplication reasoning, no new design surface. Land
provider reachability and channel state only after Sam confirms the
last-known-outcome approach (provider) and the ChannelManager-read
approach (channel), since both are choices about a command's behaviour
he already has strong, specific opinions about.

## Packages this touches (Phase 2)

- `internal/infrastructure/eventbus/nats`: `Client.Connected() bool`.
- `internal/container`: `Pool.Active() int` (and `Pool.Cap() int` for
  "N/M" display, from the already-configured `MaxConcurrency`).
- `internal/daemon`: last-poll timestamp, read accessor.
- `internal/gateway`: `formatStatus` gains the new lines; `Router` gains
  whatever read surfaces back them (mirrors `StatusReader`'s existing
  pattern -- a narrow interface, not a daemon-internals handle).
- Provider reachability and channel state: packages TBD pending the two
  decisions above.
