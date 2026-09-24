# /status health surface

**Status:** Finalised
**Beads issue:** `archie-core-wp9s`

A reported field must have a truthful source. `/status` reports broker
connectivity and the last chat-model call; the container pool, last poll and
channel state have no source in the process that answers the command, so they
are left out rather than faked.

## Background

`archie-core-wp9s` ("/status and /tasks have the wrong division of labour")
asked for two things: `/tasks` owning the live per-task work view, and
`/status` owning daemon health -- "broker connectivity, provider
reachability, channel state, container pool, queue depth, last poll,
version" -- with neither duplicating the other or static config available
elsewhere (`/model` already lists active provider/model).

## Phase 1

- `/tasks`: new command, `gateway.ChatTaskSummary` extended with
  `Stage`/`Attempt`/`ParkReason`/`UpdatedAt` (all already persisted on
  `store.Task`, zero new instrumentation), sorted actionable-first.
- `/status`: itemised per-status task counts replaced with one aggregate
  `Queue: N in flight (...)` line (running/waiting/parked only -- queued and
  terminal states excluded as not a health concern). Runtime
  (provider/model) is temporarily left in place; see "Runtime (provider/model)"
  under Phase 2's "Truthful or absent" for where that stands now.

Not touched: `/agents` (`gateway.AgentReader`/`Router.Agents`) is a separate,
pre-existing command that already has the right shape for "list active
work" but is never wired to a real implementation in any composition-root
call site (`bootstrap.go`, `telegram_setup.go`) -- it always answers "Agent
listing is not configured." in production. That is a standalone bug
(`archie-core-mxls`), not folded into this work: deciding whether to wire
it, repurpose it, or delete it in favour of `/tasks` is Sam's call, not
something to resolve by implication here.

## Phase 2 -- health checks, verified against the actual tree

Each bullet below was checked against real code before being called cheap
or not; nothing here is guessed. Both open questions were settled (see "The two
decisions"). The health section reports the broker connection and the last
chat-model call; the three facts the answering process cannot see are covered
under "Truthful or absent".

### Verified as cheap against the tree

- **Broker connectivity.** `internal/infrastructure/eventbus/nats.Client`
  already exposes `CoreConn() (*nats.Conn, error)`
  (`client.go`). The underlying `nats.Conn` (nats.go) has its own
  `Status()`/`IsConnected()`. A `Client.Connected() bool` wrapper is a few
  lines.
- **Container pool.** `container.Pool` already tracks `active int` under
  its own mutex (`pool.go`) but has no public accessor. A
  `Pool.Active() int` getter is a few lines.
- **Last poll.** Nothing tracks this. The daemon's poll loop
  (`internal/daemon`) needs one `lastPollAt time.Time` field (atomic or
  mutex-guarded, matching the pool's own pattern) set at the top of each
  poll tick, with a getter.
- **Queue depth.** Covered by Phase 1.

### The two decisions

- **Provider reachability is last-known outcome, never a live probe.** A chat
  command must not perform a blocking network call in its handler, and a probe
  reports the probe's reachability rather than the daemon's. `/status` therefore
  prints what actually happened -- the last chat-model call's model, age and
  error, or "no calls attempted yet" when there has been none. Implemented as
  `providerOutcomeRecorder`, written once in `sendChatTurn` (the single point
  every chat-model call in the process passes through) and read back by
  `newStatusHealth`. Nothing on this path dials a provider. The existing live
  model reachability check (`readiness.go`'s `modelReachProbe`) is unchanged and
  stays where it belongs: on `/health/detailed`, off the chat path.
- **Channel state comes from the existing ChannelManager.** `/status` reads
  `status.Manager.Snapshot()` -- the same ledger the dashboard and the readiness
  probe already use -- rather than re-deriving channel health from each adapter.
  No method was added to the `Channel`/`Gateway` contract.

### Phase 2

- **Broker connectivity.** `internal/infrastructure/eventbus/nats.Client` gained
  `Connected() bool`, which reads the connection the process already holds. The
  standalone Gateway (which dials NATS itself for task actions) reports the same
  fact from that connection.
- **Container pool.** `container.Pool.Active()` and `Pool.Cap()` read the pool's
  own counter and configured `max_concurrency` -- not a Docker listing, which
  would include containers this pool does not own. A cap of zero means
  unlimited, so `/status` renders the count alone rather than "1/0 active".
- **Last poll.** `daemon.Daemon.LastPollAt()` reports when the most recent poll
  pass _began_; both poll paths (`poll` for single-identity `Run`,
  `pollForIdentity` for `runIdentities`) stamp it. A pass that hangs leaves the
  stamp stale, which is the signal -- stamping completion instead would leave a
  wedged poller looking healthy.
- **Queue depth.** Covered by Phase 1.
- **Rendering.** `gateway.formatHealth` renders one line per reported fact
  (`Broker:`, `Containers:`, `Channels:`, `Chat model:`, `Last poll:`) between
  the queue line and the runtime block. A `/status` reply carries `Broker:` and
  `Chat model:`: the Gateway is the only process that serves a Router, and it
  holds neither a container pool, a poll loop nor a channel manager. The other
  three lines render when a report carries them. The broker source is a
  function rather than a captured value because the connection differs by
  composition: the daemon's shared eventbus client in one process, the
  Gateway's own task-actions connection in the other.

### Truthful or absent -- what is deliberately not reported

- **Version.** Still excluded: `/version` owns it, and duplicating it back into
  `/status` is the exact "answers something available elsewhere in chat"
  complaint this ticket exists to fix.
- **Sections the standalone Gateway process cannot see.** The daemon owns the
  poll loop, the container pool and the channel manager; the Gateway process
  owns none of them. Its `/status` reports broker and chat-model health and
  omits pool, polling and channels rather than printing zeroes. Aggregating them
  across processes would need a new daemon RPC -- a new subsystem, out of scope
  here. For the same reason the chat-model line is per process: the daemon
  records the turns it runs (Telegram, email, webhook), the Gateway records the
  web chat's, and the two do not pool outcomes. Each line is true of the process
  answering the command. **Decided: they stay per-process.** Pooling
  them would mean a new daemon↔gateway health RPC for one status line, and a
  per-process line is true where an aggregate would be a claim about a process
  the answering one cannot see. The composition root therefore builds the
  health source from the broker connection and the chat-model recorder alone: a
  producer for a daemon-owned fact would build a section no reply can carry.
- **A channel's failure reason can outlive the failure.** `status.Manager` keeps
  the last non-empty `Detail` across state changes, and that one field carries
  both a descriptor's standing caveat and a runtime failure reason, so a channel
  that failed and later recovered can still render the old reason. Splitting the
  two belongs at the producer (the manager and its descriptor), not in this
  reader, which reads the field exactly as the dashboard does.
- **Container pool when Docker is unavailable.** Composition leaves
  `containerPool` nil, so the section is absent. Naming a reason would mean
  carrying Docker's startup error into the health surface.
- **Runtime (provider/model).** Phase 1 left this block in place; it is still
  here. It is static config that `/model` and `/whoami` already answer, so it is
  the only line in `/status` that this ticket's own rule says should not be
  there -- but the maintainer reads `/status` daily to see the active model at a
  glance, and one command answering both "is archie well" and "what is it
  running on" is worth that duplication. **Decided: keep it.** It is
  a settled product decision, not an oversight; do not remove it as a
  mechanical follow-up.
- **`/agents` copy.** The menu still advertises "List tasks being
  worked" while `Router.Agents` is never wired (`archie-core-mxls`), so that one
  published description does not match what its command does. Deleting,
  rewiring or redefining `/agents` is that bead's call and is not folded in here.

## Packages this touches (Phase 2)

- `internal/infrastructure/eventbus/nats`: `Client.Connected()`.
- `internal/container`: `Pool.Active()`, `Pool.Cap()`.
- `internal/daemon`: `lastPollAt`/`markPoll`/`LastPollAt()`.
- `internal/gateway`: `health.go` (health contract + renderer), `Router.Health`,
  `formatStatus`.
- `internal/app/archied`: `status_health.go` (the composition-root bridge),
  `ProviderOutcomes` threaded through `telegramSetup` into every turn runner,
  and `sendChatTurn` recording each call's outcome.
- `internal/domain/messaging` + `internal/channels/telegram`: `/status` and
  `/tasks` descriptions, which now describe both commands' actual jobs on both
  published surfaces.
