# archied changelog

## [1.37.0] - 2026-09-22

### A request is attributed to the identity that made it

- **A browser can sign in, and an action taken through it records the person.** The dashboard validates a
  provider-issued token, resolves it to an identity, and attaches it to the request; the task-action record
  then names the actor. `Authenticate` and the sign-in callback share one resolver, so a browser sign-in
  cannot be accepted on terms a presented token would be refused on.
- **archie issues no session.** The callback stores the provider's own token in an HttpOnly cookie and every
  later request re-verifies it, so nothing archie holds can outlive the provider's decision and there is no
  session table to expire, revoke or get wrong.
- **The event kind describes the actor.** A person's approval records `human_approved` as that person; an
  agent's records `agent_approved`; an action with no authorising principal records `task_approved` with the
  attribution fields **empty**. Nothing is credited to an identity that did not act, and an unattributed
  action is recorded as unattributed rather than guessed.
- The acting identity is derived from the presented credential and **never** from a request field: a body
  carrying `actor`, `actor_id`, `identity` and an `X-Archie-Actor` header still cannot choose it.

### A host install now works, and an update refuses a host it cannot keep consistent

- `install.sh` writes and enables **all five** host units; previously it seeded only `archied.service`, so a
  fresh host ran one service of five and an operator applied the rest by hand from a runbook. The runbook is
  a description again: it names the units and points at `systemctl --user cat`.
- **Every host binary reports its version**, and the updater reads all of them **before it can report
  "nothing to install"**. A host whose binaries disagree is refused, naming each one: a newer messaging
  binary beside an older state store crash-loops against a service the older store does not serve, and that
  install previously looked healthy from the updater's point of view.
- The packaged, installer and updater command lists are pinned by a test, so a service added to one and not
  the others fails the gate instead of shipping a partial install.

### A release image can be traced to a commit

- The publish step derives the commit by **dereferencing the ref** rather than reading `github.sha`, because
  every release tag in this repository is annotated and a tag's object id is not a commit. An immutable
  `sha-<commit>` tag is published alongside `:latest`, and a build with no release tag is stamped
  `dev+<short sha>` rather than a bare `dev` that identified nothing.

### Curator definitions are data, not a second registry

- Curator definitions may come from configuration or code and run through **one** engine into the existing
  registry — no parallel authority and no second config reader. A definition declares its tools, and an
  unknown tool name fails rather than widening the set silently.

## [1.36.0] - 2026-09-22

### The dashboard declares tools an in-browser agent can call

- The dashboard registers **seven WebMCP tools**. Read-only: `list_tasks`, `get_task`,
  `recent_events`, `daemon_health`. Recovery: `retry_task`, `stop_task`, `cancel_task`. An agent
  operating the dashboard now reads structured state and recovers stuck work instead of scraping the
  UI.
- Every tool wraps the same `ui/src/lib/api.ts` client the dashboard itself calls, so an agent and a
  person exercise one path rather than two.
- `archie-ui` cannot be attributed to the agent that called it: a page can invoke its own tools, so an
  agent's call and a page script's call reach the server identically. Tools therefore record the
  signed-in identity and a source, and **never** that an agent acted.

### Binaries built by the development build task can be updated

- `task build` passed the `installtype` stamp for three of six commands, so `archie-messaging`,
  `archie-state-store` and `archie-ui` were built without it. An unstamped binary reports `unknown`
  and the self-update path **refuses rather than guesses**, which is how an operator ends up unable to
  update with `install type is unknown` and no way forward. All six are stamped now, and a test reads
  the build task and fails naming any command line that omits it.

## [1.35.0] - 2026-09-22

### The Messaging Service is severed from the store and workflow runtime

- `cmd/archie-messaging` no longer links `modernc.org/sqlite`, `internal/store`,
  `internal/domain/workflow` or `internal/agentexec`. All four arrived through a single import of
  `internal/app/controlplane`; the client half now lives in
  `internal/infrastructure/controlplanerpc`, and the app package keeps the server, the registry, the
  seeds, the validators and the schema derivation.
- The resource documents are type-aliased back into the app package, so there is one definition read
  under two names rather than a copy per side.

### Channel lifecycle reaches the dashboard

- The Messaging Service reports each channel's state, recorded **per channel**: one channel's failure
  marks only itself, a channel that cannot start is marked failed **with its reason** rather than only
  logged, and one that returns is marked stopped. `internal/channels/status.Manager` reports each channel's state.
- `PutChannelStatus`/`ListChannelStatus` carry that to the dashboard, following the config-snapshot
  split. The store **replaces the reported set**, so a channel the reporter stops mentioning is
  deleted rather than left as a stale `running`; an empty report clears the table, because hosting no
  channels is a fact worth reporting; and an id-less row is refused, since it could never be replaced
  or removed by a later report.
- `Service.ReloadChannel` routes to the channel's own seam and refuses a channel that cannot reload
  with a reason rather than accepting and ignoring it. `ReloadSupported` was previously declared **by
  name**, so a channel could advertise reload with nothing behind it; one function now decides both.

### Action playbooks: a second playbook shape, with typed results

- A playbook is now one of two shapes: a **workflow playbook** (exactly one `workflow` action,
  unchanged) or an **action playbook** (one or more `module` actions in order). A playbook does not
  mix them.
- `args` and `when` share one compilation path and one evaluation context, and `actions.<id>` is typed
  by the kind that action runs, so a misspelled result field, an unknown id, a map index, `in`, a
  comprehension, or a shape-foreign key (`kind` on a `workflow` action, `workflow` on a `module`
  action) is a **load failure** rather than a dispatch failure.
- **Action playbooks load but do not run yet.** Routing arrives with the first side-effecting
  position; the daemon warns at load rather than accepting them silently.
- The CEL environment is built per playbook, because result typing depends on the ids a playbook
  declares.
- `ModuleRegistry.DecodeResult` turns a kind's flat map result into its typed `Result` before a later
  expression reads it. Without it a typed read evaluated against a shape the run never produces, and
  so silently read null.

## [1.34.0] - 2026-09-22

### Dashboard: an actionable authentication failure state

- An unauthenticated **document** request now receives a self-contained HTML page with a
  paste-the-token form, instead of plain text. Previously the whole dashboard -- `index.html`, the
  bundle and every asset -- sat behind the token check, so a bookmarked bare URL returned a
  `text/plain` "unauthorised: open the dashboard URL archied logged at startup", the SPA never ran,
  and none of the dashboard's existing failure surfaces were reachable at all.
- The response is still **401**, with `Cache-Control: no-store` and a CSP. It is deliberately not a
  200: a login page served as success would be cacheable, would read as healthy to anything that
  checks status alone, and a shared cache could hand it to the wrong person.
- API and stream callers are unaffected. `application/json`, `*/*` and `text/event-stream` requests
  keep the plain-text 401, with a test pinning that split so an XHR or an `EventSource` reconnect can
  never start receiving a page.
- The auth banner gains a **Retry** action that re-runs the dashboard's bootstrap reads, so a
  recovered session is actionable without a browser-level reload. It deliberately does not re-drive
  the SSE stream, which already reports its own reconnect state.
- The page's guidance stays generic: it points at the dashboard URL the daemon logs rather than
  naming where the token is stored, so the HTTP layer is not coupled to the token file's location.

## [1.33.0] - 2026-09-22

### EDA: playbook action arguments are compiled at load (J2)

- A playbook action may now declare `args`, compiled as CEL expressions when the playbook loads.
  `args` and `when` share one compilation path and one evaluation context, so an expression written
  for one works in the other.
- **Behaviour change:** an `args` expression that does not compile is a **load failure**, not a
  runtime miss — the same reject-at-load rule already applied to a binding collision and to an
  unknown `actions.<id>`. In practice, a daemon loading a playbook with a malformed `args` block
  fails at startup rather than part-way through a dispatch.
- Compiling several arguments now reports the **first** bad key deterministically (keys are sorted).
  Previously it ranged a map, so the reported bad key could differ between runs and the failure was
  not reproducible.
- `args` is deliberately **inert** for now: parsed, compiled and stored, but no shipped position
  consumes it yet. A consumer arrives with the first side-effecting position.

**Not yet decided:** how an `args` evaluation failure behaves at dispatch. The `when` predicate rule
(error, skip, log) does not obviously transfer, because `args` is data rather than a predicate, and
silently skipping an action whose arguments failed to evaluate would be a silent failure of exactly
the kind this release train has been removing. It currently propagates the error to the caller, and
that asymmetry is documented at the code. Ratifying it against a real consumer is tracked separately.

## [1.32.1] - 2026-09-22

### Host packaging: `archie-messaging` now ships and updates

- `cmd/archie-messaging` is included in the distribution zip, and `scripts/archie-update-install`
  installs and restarts it.
- **Why this mattered.** v1.30.0 moved Telegram, email and webhook out of `archied` and into
  `archie-messaging`, but nothing packaged the new binary. A host install that upgraded across
  v1.30.0 therefore ran on with **no messaging process at all**: `archied` logged that it stopped
  serving the telegram gateway, nothing replaced it, and Telegram stayed dead — with no error
  logged anywhere, because nothing was failing. The process simply did not exist.
- The host component set was enumerated in four places — the zip's build loop, the zip's copy list,
  the installer's service list, and the installer's binary list — and `archie-messaging` was missing
  from all four. The duplication was the defect: a single test now derives the host-run commands
  from `cmd/` and asserts that all four lists agree with it, so adding or extracting a process fails
  until it is packaged.
- `deployments/systemd-user-service.md` gains the messaging unit. That is load-bearing rather than
  cosmetic: the updater refuses an update when a service unit is missing and tells the operator to
  create one from that document, which previously had nothing to offer.

**Upgrade note:** a host that reached v1.30.0 without a messaging unit has been running with no
Telegram, email or webhook. Updating to this release installs and starts `archie-messaging`.

## [1.32.0] - 2026-09-22

### EDA: playbook action ids, and a dispatch idempotency ledger

- **Action ids (J1).** Every action a later action references must now carry an `id`. Ids are
  validated at load -- unique and stable-identifier-shaped -- and a `when`/`args` expression
  naming an unknown `actions.<id>` is a **load failure**, not a runtime miss, the same
  reject-at-load rule already applied to a binding collision.
- **Reference classification is strict at load.** A map-index reference (`actions[expr]`), a
  non-literal index, and a reference to an unknown prior id are all rejected when the playbook
  loads; references are classified by identifier versus static access, and the when-compilation
  helper is shared between the load-time and runtime paths.
- **Dispatch idempotency ledger.** A `playbook_dispatches` row records a dispatch against
  `(playbook_id, playbook_version, event_id, action_id)`, written with `INSERT OR IGNORE` and
  surfaced through the shared `store.ErrAlreadyDispatched` sentinel. Module, channel and forge
  action positions are therefore at-most-once, which is what unblocks the side-effecting
  positions that were previously rejected at load.
- **The ledger is wired into production.** `stateStoreDeps` constructs `Deps.PlaybookDispatcher`,
  so the ledger RPCs no longer answer `Unavailable` in a running daemon. This defect was found
  by the pre-release review of this change rather than by its tests.

**Behaviour change:** a playbook whose actions reference `actions.<id>` without declaring that
id, declare a duplicate id, or index `actions` with a non-literal value now fails to load
instead of failing at dispatch time.

## [1.31.0] - 2026-09-22

### Dashboard replaced: Preact -> Vue 3 + shadcn-vue

The dashboard is now a Vue 3 application built on shadcn-vue. This is the
large piece of this release; it touches every page.

- History routing, a grouped topbar built from components, the violet palette
  with self-hosted Geist, and a command palette with live tasks.
- Chat is a FAB-opened panel that behaves like a chat window and scrolls its
  transcript with `MessageScroller`.
- Task actions and dangerous approvals are deliberate; the task detail page is
  a single head strip with honest states; the run page settles its head around
  an attempts pager, which is hidden for zero or one attempt.
- Appearance and System navigation were refined, with a four-column appearance
  grid on wide screens.
- A copy pass removed redundant tails and restating descriptions, empty states
  derive their own titles, and a stuck log read surfaces as an error rather
  than eternal loading.
- The capture pane is bound to the list it sits beside and reads beside it,
  not over it.

### Events is one page with three tabs

- Inspector, Bindings and Mappings are now tabs of a single **Events** page.
- The nav entry is always live, the tabs prune by capability, and the three old
  paths survive as bookmarks that redirect to their tab.
- Tab selection lives in one module (`ui/src/events/tab-selection.ts`) that the
  page uses, rather than a copy of it.
- `feat(tools)`: a capture-intake sender exists for exercising the Events
  surfaces.

### The channel travels with the message

`SessionSource.Platform` -- the first component of the session natural key, and
the column `GetByChannel` filters on -- was a constant `"web"`, because one
Router serves every channel and named itself after its own. Two things followed:
the per-user identity policy was real code that no production call site reached
with a real channel name, and a curator pass's write to agent-user memory could
not be read back by the turn that wrote it, because that turn resolved no
`UserID`.

- The frontends now name the channel they carry (`telegram`, `email`,
  `webhook`, `web`), the channel rides the message, and the Gateway stamps the
  session from it -- falling back to its own name when a sender names none,
  which is what every existing row holds.
- Identity resolution reads the platform off the inbound, so `carriesPersonIdentity`
  decides something in production: telegram and email resolve a person, the
  dashboard and a webhook resolve nothing, and an unknown platform fails closed.
- **Behaviour change:** a chat whose active session came from `/new` or
  `/branch` re-resolves once, because session lookup now filters on the
  channel's real name. A chat without `/new` re-resolves onto the same
  deterministic id and heals silently.
- Deliberately unchanged: `TurnRunnerConfig.Channel` still keys tool
  availability, so no channel gains or loses a tool in this release.

### Control plane: runtime administration, history, and derived schemas

- The State Store's gRPC server now carries runtime administration, and each
  process reports which settings version it is running; the dashboard shows it.
- Resource history is exposed so edits can be reviewed and rolled back, and
  resource schemas are derived from the documents they describe -- the document
  editor reads the derived schema (labels from `title`, `format: duration` ->
  `1m0s`, hints from `doc`), and the abandoned schema-driven card is deleted.
- A live settings replacement is checked before it is switched to, the daemon's
  state surfaces are folded into one boot step, and a SIGHUP reload re-applies
  database-owned settings.
- Forge intake settings that nothing reads are now rejected at load.

### Configuration: judged where it is owned

- The runtime config overlay is deleted; database-owned settings are judged at
  the layer that owns them rather than on the file load path.
- A file overlay no longer drops the fields it omits inside a map entry, and a
  kind with no stored value leaves the file's value in force.
- The default config path is resolved in one place, and the channel document
  has its own shape.

### State Store: offline backup, restore, validate and rollback

- New offline backup / restore / validate / rollback path.
- `validate` runs boot's own gate; it no longer replaces the database, reads a
  kind the store does not hold as its seed, or touches the daemon's log.
- The recovery control-plane server is built on the shared step vocabulary.

### Workflow: one step vocabulary and a `gate.diff-rules` replacement

- The step vocabulary is owned by a family manager registered at each
  composition root, replacing `.archie/gate.go` with `gate.diff-rules`, which
  fails closed on a change it never read.
- Workflow roots are pinned and the dead vocabulary is removed; the workflow pin
  crosses the wire.
- The schedules resource skips the legacy cron jobs it cannot hold, and the
  scheduling policy carries the label its trigger pairs with.

### EDA: the playbook coordinator reaches production selection

- The playbook coordinator is wired into production workflow selection, so a
  matching playbook decides a real task's workflow at the daemon's definition
  pin, inheriting idempotency from `TaskEnvelope`.

### Reliability and operations

- `cronstore` writes a schedule interval as a duration, not nanoseconds -- a
  persisted `1m` no longer round-trips as `60000000000`.
- Worktrees are cut from the mapped base branch, and the Gateway's worktree
  manager is built so it serves `review_pr`.
- A late max-uptime callback no longer resurrects teardown state.
- The state store dial installs client keepalive, and its server policy accepts
  the dialer's pings; control-plane watch streams re-establish themselves after
  they end.
- `/status` serves only the health facts a reply can carry; the daemon reports
  systemd readiness and loop-driven watchdog heartbeats.
- A webhook route path no longer becomes a sender identity.
- A chat session compresses when a turn crosses its context budget.
- The MCP client supports per-server `parallel_tool_calls`, carried through the
  tool-settings projection.
- Resource request-ID idempotency keys are scoped to their kind.
- The session curator addresses memory by the deployment's bot user.

## [1.30.0] - 2026-09-20

### Messaging Service extraction completed

Telegram, email and webhook no longer run inside the daemon. They run in
`archie-messaging` and reach the daemon through the Gateway's `ChatContract`
over gRPC; the daemon builds no channel and no `gateway.Router` (#899).

- The operator surface is re-wired in the extracted service: `/update` (built
  from its own `[chat.telegram]` commands), the update-report relay, the
  `show_tool_calls` projection, and `/restart`'s config reload, which
  re-resolves the token and allowlist from this service's own file and refuses
  a reload that resolves to an empty token.
- `/version` is sourced from the Gateway through a new `ChatSnapshot.Version`
  field. `cmd/archie-messaging` deliberately takes no version ldflags, so it
  cannot report a partial upgrade as a matched one.
- Channel configuration is validated at composition, so a configured-but-
  invalid front-end fails the service instead of being silently dropped.
- The inbound rate limiter moves to the Gateway, which now owns the only
  Router and is therefore the one place a per-sender budget can apply to every
  channel's turns.

`RunningVersions` and `ReleaseAnnouncements` are deliberately left unwired:
both need a component's real running version, and the only versions this
process can obtain are another binary's stamps. See
`docs/architecture/migration-decisions.md`.

### Configuration

- `diff_cap_lines = 0` now switches the cap off, as the dashboard and the
  workflow step have always documented. It was a plain `int`, so the defaults
  pass could not tell an absent key from an explicit `0` and rewrote both to
  400 -- the documented way to disable the cap was unreachable. **Behaviour
  change:** a config that set `0` was silently getting 400 and will now get no
  cap.
- The same ambiguity was failing open per identity: `configForIdentity`
  assigned the identity's value unconditionally, so an identity that merely
  omitted `diff_cap_lines` overwrote the shared cap with the zero value and
  disabled the safety rail entirely. An identity now overrides only when it
  sets the key.
- A diff-cap park no longer tells the operator to "approve manually". A parked
  task's actions are retry, abandon and reject, so there is no approve path out
  of diff-cap; it now names the two things that work.
- `[health].dependency_timeout` makes the daemon's readiness probe timeout
  configurable, matching the extracted services.

### Chat rendering

- `/status` and `/tasks` are line-oriented reports but separated lines with a
  plain newline, which is CommonMark's soft wrap: the Telegram renderer joined
  them into one run-on paragraph. The renderer implements CommonMark's hard
  line break and the reports use it. Ordinary prose still soft-wraps.
- A park reason is capped to its first line in the `/tasks` list. It carries
  whatever the failing stage reported, which for a lint gate is a whole tool
  log; unbounded, one parked task pushed every other task out of the reply. The
  full reason is unchanged on the task, in `task_list` and on the dashboard.

### Review

- Review findings are posted as line-anchored PR comments with suggested fixes,
  and a suggestion must be single-line -- GitHub applies a multi-line suggestion
  only up to its first line, silently corrupting the file (#891).
- The remediate workflow handles PR review comments and requested changes.

### /status health surface

`/status` reports the live broker connection and the last-known outcome of this
process's chat-model calls, alongside the queue depth and runtime block (#892).

- A provider error is flattened and capped, so a multiline or oversized error
  cannot break the one-line-per-fact layout.
- Concurrent model calls no longer reorder: the completion time is taken before
  the lock, so the newer outcome wins rather than whichever goroutine stored
  last.
- Curator model calls are recorded too. They bypass the chat path, so a daemon
  whose only recent model traffic was curator work reported "no calls attempted
  yet" while the provider was demonstrably reachable.
- Title generation does not record health. It runs detached under its own
  bound and its error is swallowed, so a cosmetic timeout was overwriting the
  line with "failed".

**Known limitation:** the container, poll and channel sections cannot render.
Those facts live in the daemon, but the only Router lives in the Gateway
process, which has no container pool and no poll loop. The command description
says what actually appears; restoring the rest needs a cross-process health
contract.

### Dashboard and containers

- The served lifecycle vocabulary reaches an already-painted page, the page
  registry matches the routes, and the build verifies `ui/dist` is fresh
  instead of never looking at it (#890, #898).
- The container reaper no longer deletes teardown bookkeeping early, so a
  released container is not torn down twice (#893).

### Build

- `task fmt` runs `golangci-lint fmt` -- the same engine and configuration the
  gate checks with. Standalone `gofumpt` cannot reproduce gci's import
  grouping, so formatting and linting could not converge and no command
  satisfied the gate (#901).
- `goimports` is deliberately absent from the formatter set: it re-reads and
  re-parses the module index once per file, ~135s across this tree.

### Fixed

- `Config.Clone` left the new diff-cap pointers aliased, so a clone could write
  through to the published snapshot (#902).
- A curator asking for a completion on a daemon with no configured provider
  panicked the process; it now returns an error.

## [1.29.0] - 2026-09-19

### Security

- `google.golang.org/grpc` bumped past `v1.84.0` to patch
  [GO-2026-6443](https://pkg.go.dev/vuln/GO-2026-6443), a server panic via a
  missing `:authority`/`Host` header reachable through `archied`'s gRPC
  gateway server. No stable release carries the fix yet, so this pins the
  pre-release commit that does (`v1.85.0-dev.0.20260825072537-93e31b48545e`)
  until `grpc-go` cuts `v1.85.0`.

### Memory engine unification

One memory engine, addressed by four typed scopes (global, agent, user,
agent-user), replaces the two disconnected stores that previously existed:
a chat-tool-written store nothing ever read back, and a curator-written store
with no scope model.

- `internal/domain/memory` is a CRUD contract (`Create`/`Get`/`Query`/`List`/
  `Update`/`Forget`/`Revisions`) with retained revisions; updates supersede
  rather than overwrite, so a crash mid-write leaves a recoverable extra
  history entry, never a lost prior state (#886).
- The chat turn reads its subject's scopes synchronously before the prompt is
  built, rendering recalled records into a `<memory>` block; a failing or
  panicking engine degrades to no memory block rather than failing the turn
  (#887).
- The model writes memory through four id-addressed tools
  (`memory_create`/`update`/`delete`/`list`), scoped to the turn's own
  resolved identity -- the model chooses the scope kind, never the ids, so it
  has no path to write into another user's memory (#888).
- The session curator now derives the agent-user scope from the session's own
  user-role messages, instead of keying facts by a session id that stops
  meaning anything once the session ends.
- A channel with no resolvable user (dashboard, webhook) reads and writes only
  global and agent scope; a webhook's route path is never treated as a user
  identity.
- Fixed a review finding on the read path: the per-scope record limit was
  being applied as one shared total across all scopes rather than per scope,
  and the rendered block's byte cap was checked before template escaping
  rather than after, letting the escaped block exceed it.

### Work intake

- feat(workintake): define review reaction envelope
- feat(workintake): authorize review reactions by owned PR

### Cleanup

- Deleted the unwired plugin capability-host health surface
  (`Host.Health`/`Host.Manifests`/`ModuleStatus`) and the `Module.Health`
  chain it fed -- including the `mcp`/`builtin` tool-provider health
  implementations and their aggregation in `tools/provider.Registry` -- none
  of which had ever had a production caller (#882).

## [1.28.0] - 2026-09-19

### Task detail and attempt attribution

- New per-run task detail page, showing each attempt's own events, changes and
  configuration rather than a single merged view.
- Events, changes and config are attributed to the attempt that produced them;
  `Event` carries `attempt` and `ReadTaskLogRequest` carries `stage`.
- The task detail page remounts when the task id changes, so switching tasks
  does not leave the previous one's data on screen.

### Configuration

- Services are keyed by name through a registry, so the State Store and Gateway
  listen addresses are configurable rather than fixed.
- A partial overlay no longer clears the fields it omits — an overlay that set
  one key was wiping the rest.
- The Services map is cloned on publish, so an overlay can no longer mutate the
  snapshot other readers are holding.
- The service context is enforced at registration.

### Logging and park reasons

- A parked run's reason is recorded in its own attempt log, in both the daemon
  and the workflow, so "why did this park?" is answerable from the task itself.
- `TaskRegistry.Write` takes the caller's context.

### Worktrees and containers

- Retries recover from path-type conflicts instead of failing opaquely, and the
  underlying cause is no longer masked.
- The worktree is handed back on every sandbox exit.
- A container's max-uptime reaper is cancelled on release, the cap stays active
  through the grace period, and teardown is claimed once rather than by
  cancelling a timer.

### Daemon and startup

- Every identity's forge and repos are swept at startup, including the root
  forge.
- A cancelled startup is no longer reported as an exhausted sweep budget; an
  exhausted budget names itself as the cause.

### Other

- `grep` can use the workspace codesearch index.
- Forge labels are matched when namespaced, so label-driven routing works for
  namespaced labels.
- Triage is given criteria for choosing between workflows, and a workflow is
  required when a code change is needed.
- The Telegram adapter restart callback is wired into the chat router.
- The `ARCHIE_REACTIONS` fan-out stream is provisioned and bounded by age.
- `archie-ui` exits cleanly when graceful shutdown overruns its deadline.
- The dashboard's event pump retries priming until the State Store answers.
- The update writer emits the agent version sidecar with a real newline.
- The UI finishes its Preact port; the imperative DOM layer is deleted.
- Collection fields decoded over the State Store gRPC contract are never nil
  slices, so an empty collection marshals as `[]` rather than `null`.

## [1.27.0] - 2026-09-17

### Dashboard task logs

- Task logs are readable from the dashboard again, through a new State Store
  read contract (`ReadTaskLog`, `StreamTaskLogContent`) rather than the UI
  process reaching into the daemon's log files. Wired server-side by the
  standalone state-store process.
- Added `GET /api/tasks/{id}/logs/download`, serving an attempt's raw log as a
  `task-<id>-attempt-<n>.log` attachment.
- A process that cannot read logs and an attempt that recorded no log are now
  distinguishable ("cannot read" answers 503, "no log" answers 404) instead of
  both rendering as a claim about a configuration setting.

### Telegram output

- Tool results are summarised rather than quoted: a search reports how many
  matches came back, a truncated result reports the tool's own total. The
  renderer no longer states a count it computed from its own truncated sample.
- Repeated successful tool calls collapse into one counted entry.
- The tool-activity block is capped to a third of the live frame, keeps the
  newest lines, and marks what it dropped with a `+N earlier` indicator. Every
  line that fits is kept.
- Every clamped answer marks its cut with an ellipsis, including the opening
  frame, which previously appeared to begin mid-sentence.
- An error response from the log download no longer ships as a `.log` file.

### Readiness and shutdown

- The disk probe reports per-filesystem results and checks the root filesystem
  as well as the work directory, so a full root is no longer invisible.
- A path shared by several disk targets keeps the strictest requirement any
  target places on it, so an optional target can no longer mask a required one.
- The shutdown-watchdog test no longer races the Go scheduler, which was
  intermittently failing the quality gate and parking work unrelated to it.

### Other

- Messages persist the channel-native sender id.
- go-telegram/bot bumped to v1.27.0, fixing a streaming bug where the multipart
  body could not be replayed and `sendMessage`/`editMessageText` failed until
  the connection dropped.

## [1.26.0] - 2026-09-17

- fix(webui): publish per-identity forges so multi-identity task rows keep their links
- feat(config): add [scheduling] config block
- feat(configuration): translate [scheduling] into scheduling.EngineConfig
- feat(cronevents): adapt the events bus to scheduling.Sink
- fix(webui): count every chat front-end in the published projection
- fix(scheduling): respect configuration ownership
- feat(ratelimit): wire per-identity inbound rate limiting into the gateway
- fix(tools): judge the rm guard by base name so absolute paths cannot bypass it
- fix(store): keep an empty binding secret empty so a partial edit cannot erase it
- fix(archied): stop advertising review_pr when no reviewer can run
- fix(daemon): carry kind/label routing bindings to the process that routes
- feat(configuration): add archied setup, the caller the first-run flow was built for
- fix(configuration): stop letting a secret value be set from the command line
- feat(configuration): let setup reference a secret without carrying its value
- fix(archied): disable a provider whose credential is missing instead of refusing to boot
- refactor(install): install.sh delegates config generation, and tests stop reading hand-written configs
- fix(daemon): guard binding dispatch loop against nil dispatcher/creator
- fix(webui): enforce CSRF/origin gate on binding and mapping mutations
- fix(secret): recover a panicking secret engine instead of crashing
- fix(config): deep-copy Image.Hosted/Image.Local in Config.Clone
- fix(email): stop dropping the first paragraph of plain-text bodies
- fix(logging): keep the live descriptor open across a failed rotation
- feat(configuration): report config keys no decode target consumed
- feat(archied): warn at boot when the config file has unrecognised keys
- feat(archied): wire the scheduling ticker engine at boot
- fix(agentworker): wire configured tool-result policy into worker's LoopRunner
- refactor(staterpc): delete the superseded single-token auth interceptor
- refactor(memory): delete the unreachable <memory> fence scrubber
- fix(channels): delete unreachable Telegram webhook delivery mode
- fix(gateway): delete unwired Router.Dangerous approval path
- refactor(gateway): delete orphaned SessionState goal/steer/queue machinery
- refactor(archied): delete the daemon's unused webui.Server and UI adapters
- refactor(tools): delete unreachable dispatch/gating engine
- feat(channels): wire chat.webhook route config through to the gateway
- refactor(tools): delete unreachable AST-based tool discovery
- refactor(tools): delete unused plugin-tool registration API from builtin.Registry
- feat(channels): wire ConfigSchema/ValidateConfig into a startup validation loop
- refactor(state): delete the dead IncrementRetryCount RPC (breaking change, accepted)
- fix(curator): populate PassInput.Since with the prior pass time
- fix(memory): install the threat scanner the composition root never wired
- fix(staterpc): preserve marshal_error marker in eventDataJSON
- fix(gate): validate finding level and share the blocking threshold
- fix(scheduling): record a completed run so a recurring job stops re-firing every tick
- fix(releaseupdate): don't treat empty available as deferred
- fix(tools/provider/mcp): preserve MCP binary tool results for delivery
- refactor(gateway): share sensitive-key markers with webhookguard
- refactor(daemon): remove unreachable SQLite poll/drain path
- refactor(tools/mcp): delete unreachable MCP adaptation path
- fix(taskactions): resolve max_retries from the task's owning identity
- fix(daemon): forward configured MCP servers to task agents
- refactor(skill): delete dead Discover/DiscoverPlugins/Skill path
- refactor(skillbuild): delete dead AugmentRegistry path
- fix(builtin): bound read/edit filesystem work by DefaultToolTimeout
- refactor(binding): delete dead Matcher.Overlaps method
- refactor(forge): delete dead CreateIssue surface
- fix(mcp): cancel in-flight SSE reconnect on Stop
- fix(container): enforce MaxUptime lifetime cap with a timer
- refactor(store): use taskstate status constants in SQL
- fix(ui): unbreak binding approve and the two deletes, and centralise the client
- fix(tools): stop testing on the text of other files, and ungate contractaudit
- refactor(forge): delete the unwired RepliesAfter surface
- refactor(config): delete the two decoded-but-unread policy fields

This release is dominated by dead-code removal and the completion of the
scheduling capability. Roughly half the commits delete surfaces that no longer
had a caller: the unreachable SQLite poll/drain path, the unwired MCP
adaptation path, the AST-based tool discovery, the superseded single-token auth
interceptor, and several dead forge and binding methods. The
`IncrementRetryCount` State Store RPC is gone with them -- a breaking change to
the wire contract, accepted because nothing called it.

The cron/scheduling epic lands its remaining slices: the `[scheduling]` config
block, its translation into the engine's own settings, the events-bus sink
adapter, and the boot wiring that starts the ticker. A recurring job now records
a completed run, so it stops re-firing on every tick.

Operator-facing work: `archied setup` gives the first-run flow the caller it was
built for, configuration no longer accepts a secret value from the command line,
and a provider whose credential is missing is disabled rather than refusing to
boot. A secret engine that panics is now recovered instead of crashing the
daemon.

## [1.25.0] - 2026-09-15

- fix(update): resolve releases without a checkout, and fail loudly when it cannot
- fix(update): back up the task store the daemon actually uses, or refuse
- fix(update): build each component from its own approved release tag

Components are independently versioned, so the newest `archied/v*` and
`archie/v*` tags usually sit on different commits. The installer required both
tags to resolve to one commit whenever both components changed, which refused
every update once the two newest releases diverged -- a deployment stuck on
1.22.0 could not take any update. Each component now builds from its own
approved tag.

## [1.24.0] - 2026-09-12

Makes 1.23.0 deployable. That release split archied into separate processes,
but the self-updater still installed only the `archied` binary and restarted
only `archied.service`, so updating to it left the State Store, Gateway and UI
on the previous release. archied cannot boot without a reachable State Store,
so the update failed its health check and rolled back. Update to this release
rather than to 1.23.0.

If your host does not yet run the four units in
`deployments/systemd-user-service.md`, create them first: the updater now
refuses, naming what is missing, instead of installing binaries onto a host it
would leave broken.

- fix(update): install and cycle every process in the release
- fix(telegram): stop the relay corrupting identifiers and code

Also in this release, though outside the version-stamped package closure:

- ci(deploy): produce a complete distribution zip on every release. No zip was
  built for v1.23.0 at all, and the zip omitted `archie-ui`.

## [1.23.0] - 2026-09-12

Archied is no longer a single process. The State Store (`archie-state-store`)
now owns `archie.db` outright, the Gateway serves its contract over
authenticated gRPC, and the dashboard ships as a standalone UI Service
(`archie-ui`). Upgrading is not a binary swap: these processes must be running
and addressable before `archied` will start clean. See `deployments/` for the
supported profiles.

### Service decomposition

- feat(contracts): add gateway service seams and protobuf toolchain
- feat(gateway): extract ChatContract service
- feat(gateway): delete in-process ChatContract path, make remote the only mode
- feat(gateway): authenticate the gRPC contract and allow off-loopback listeners
- fix(gateway): drop services.gateway.mode, remote is the only transport
- feat(state-store): generalize storerpc as the State Store gRPC contract (#770)
- feat(state-store): stand up archie-state-store binary and own archie.db (#771)
- refactor(state-store): migrate capture/mapping/binding consumers to State Store (#772)
- refactor(state-store): migrate workflow/intake/task lifecycle consumers to State Store (#773)
- refactor(state-store): cut daemon/Gateway over to the remote State Store (#774)
- fix(state-store): task-scope container credentials, stream large capture lists, validate Update
- feat(archieui): standalone UI process over remote Gateway and State Store
- refactor(archied): cut the dashboard over to the archie-ui process
- refactor(ui): sever the archie-ui binary from every daemon runtime package
- refactor(archied): move the update relay to the process that owns updates
- refactor(archied): move webhook capture intake to the process that owns work intake
- feat(archieui): mount the capture intake receiver over the State Store
- refactor(workflow): relocate Task/Status/Source and define workflow.Store (#754)
- refactor(store): move the producer-owned contracts to internal/contracts/store/v1
- feat(messaging): migrate gateway session persistence onto domain types
- feat(gateway): flip channel boundary onto messaging.Message

### Pull request review

- feat(gateway): operator-triggered PR review with selectable reviewer model
- feat(forge): pull request review read surface (ListReviews/ListReviewComments/ReplyToReview)
- feat(workflow): surface adversarial review findings on the PR body
- feat(workflow): review calibration, checked properties and style nits out of contract
- fix(archied): share one PR reviewer across channels; harden forge and worktree
- fix(workflow): order findings before footer; test the report stash

### Dashboard

- feat(webui): render configuration from a published snapshot
- feat(staterpc): carry the dashboard's configuration snapshot
- refactor(webui): take configuration off the dashboard server
- feat(webui): report which dashboard sections a process can serve
- feat(webui): deliver live dashboard events by polling the State Store cursor
- feat(webui): route dashboard task actions through the Gateway contract
- feat(gateway): give the dashboard operator its own task-action contract
- feat(webui): add /health and /health/detailed readiness endpoints (#717)
- fix(webui): distinguish refused vs broken task actions, handle 401 (#753)

### Daemon, gateway and workflow

- feat(archied): add shutdown watchdog that force-exits a hung graceful shutdown (#716)
- feat(archied): serve the daemon's own health endpoint
- feat(drain): honour external drain marker with instantiation epoch (#718)
- feat(gateway): wire progressive tool disclosure (bridge tools)
- feat(gateway): render workspace, managed repos and operator into chat env
- feat(gateway): add session_transcript tool for full JSON export
- feat(gateway): consolidate task actions and repair daemon default path
- feat(eda): thread Playbook ID/Version and DispatchInput task identity
- feat(worktree): Resume re-syncs a worktree onto its branch tip
- fix(staterpc): attach the bearer token to State Store streams
- fix(archieui): resolve service tokens from env, without requiring a config file
- fix(archied): initialize conversation persistence on daemon startup
- fix(archied): preserve channel turn ledger with remote gateway
- fix(gateway): share NATS and session ownership
- fix(update): probe the dashboard's real address after a self-update
- fix(store): migrate binding owner and repo columns

## [1.22.0] - 2026-09-03

- feat(telegram): support secret refs for bot tokens (#705)
- fix(update): make daemon rollback schema-safe (#704)
- fix(archied): preserve ephemeral gateway ports (#707)
- fix(webui): bound log limit parsing and add endpoint regression coverage (#706)
- fix(deps): upgrade x/crypto to v0.56.0 for security fixes (#708)

## [1.21.0] - 2026-09-03

- feat(workflow): make kind-to-workflow routing YAML-overridable
- feat(workflow): route arbitrary forge labels to workflows via playbook YAML
- feat(workflow): load kind/label bindings from multiple playbook directories
- fix(extract): regenerate stale Yaegi symbol tables; fix wfextract directive
- feat(eda): Module position first slice -- registry + side-effect-free log kind
- feat(eda): CEL expression environment (compile/validate at load, evaluate at dispatch)
- feat(eda): playbook document + event coordinator, single-action workflow-kind dispatch
- fix(skillcurator): document and scope the intentional nilerr in reviewOne
- fix(binding): add Owner/Repo pin, fixing multi-repo dispatch
- feat(webui): bindings dashboard UI; fix app-wide Preact render regression
- fix(binding): split ValidateForUpdate so edits can preserve the secret
- refactor(ui): extract Pill to ui/src/base/, closing archie-core-mlro
- refactor(eda): extract helpers to satisfy cyclop/gocognit gates
- fix(webui): stretch HEALTH row cards to equal height
- fix(eda): make playbook key collision reporting deterministic
- fix(gateway): split /status (health) and /tasks (work) apart

## [1.20.0] - 2026-09-03

- fix: baseline gate repair (go test ./... -count=1)
- fix(logging): propagate bound slog attrs to wrapped handler in FeedHandler
- feat(image): define provider-neutral image capability contract and config
- fix(channels): call MarkRunning once a channel is actually up
- fix(workflow): stop discarding baseline-fix commits as 'no changes'
- fix(webui): wire the Logs page to per-task attempt-log detail
- fix(workflow): net cache hits from token totals, trim gate-retry output
- feat(workflow): dynamic triage instead of defaulting every task to implement
- fix(gateway): stop SOUL from forging the prompt invariant tags
- fix(curator): stop curator actions from serializing a zero timestamp
- feat(webui): hand-author the configuration field descriptor catalog
- feat(webui): attach the config field schema to GET /api/config
- feat(webui): render scalar config sections generically from the schema
- feat(webui): surface allow_concurrent/max_retries/review_enabled on the repositories card
- feat(webui): inline editing for per-repo allow_concurrent/max_retries/review_enabled
- feat(webui): playbook bindings -- matcher + mapping + workflow, HMAC-gated
- fix: resolve golangci-lint funlen, nilerr, and empty branch issues
- feat(store): encrypt playbook binding secrets at rest
- feat(webhook): harden captured payload redaction
- fix(store): satisfy makezero when building cipher payload
- feat(webui): refactor chat module to Preact component and add unit tests

## [1.19.10] - 2026-09-02

- feat(curator): session-memory curator reference implementation

## [1.19.9] - 2026-09-02

- feat(memory): typed memory engine contract, registry, and registrar
- feat(memory): builtin engine implementing the domain/memory contract
- feat(config): config surface for memory engine selection
- feat(daemon): wire the domain/memory engine registry at boot
- feat(curator): bind curators to memory engines
- fix(config): reject memory.provider instead of silently ignoring it
- feat(curator): skill curator reference implementation

## [1.19.8] - 2026-09-02

- fix(update): drop dev-facing wording from the unconfirmed-update message
- fix(telegram): stop adjacent rich blocks from rendering glued together
- fix(telegram): send undelivered-media fallback after finalize as its own message

## [1.19.7] - 2026-09-02

- fix(logging): make Tail/Page allocation bound explicit for CodeQL

## [1.19.6] - 2026-09-02

- fix(build): stamp gatewayVersion/runtimeVersion in the right package

## [1.19.5] - 2026-09-01

- feat(webui): dashboard-aware chat — Archie sees the page, points you there
- fix(telegram): drop retry_after bandaid, bump go-telegram/bot to v1.24.0
- feat(curator): track and expose recent activity per curator
- fix: go.mod bumped to 1.27.0 and stupid tests removed
- refactor: remove unnecessary AI-generated scaffolding
- refactor: go modernisation fixes
- refactor: remove unused Operator param.
- fix(gateway): fix broken system prompt template and scrub real telegram ID from tests
- fix(plugin): add missing CLAUDE.md reference to plugin engine rule
- fix(app): repoint archied wiring contract test at internal/app/archied
- refactor(agentexec): use ai-sdk NewTypedTool for pluginToolSet and scriptToolSet
- fix(config): standardize Provenance to pointer receivers (recvcheck)
- refactor(lint): reduce cyclomatic/cognitive complexity in tools, workflow, webui
- fix(app): repoint capability-host wiring contract test at internal/app/archied
- refactor(lint): extract more helpers to clear nestif/gocyclo findings
- refactor(lint): split AgentStage.Stage into resolveModel/buildRequest/handleResult
- refactor(lint): split TurnRunner.Run and dispatchLocal further
- refactor(lint): extract markdownBlockParser and list/quote helpers in richblocks.go
- refactor(lint): extract Apply/findWalkVisitor helpers; go fix modernization
- fix(lint): noctx fixes and dead-code cleanup
- feat(workflow): adversarial self-review stage before PR open

## [1.19.4] - 2026-08-28

- refactor(telegram): render replies as structured rich blocks

## [1.19.3] - 2026-08-28

- feat(tasks): serve the lifecycle presentation catalog from /api/task-meta
- feat(dashboard): render task actions, statuses and filters from the server catalog

## [1.19.2] - 2026-08-28

- feat(tasks): allow rejecting a task from any non-terminal state

## [1.19.1] - 2026-08-27

- fix(telegram): keep Markdown headings/lists as blocks and strip Markdown on plain fallback

## [1.19.0] - 2026-08-28

- feat(logging): cursor pagination and filtering for task_logs
- build(deps): upgrade github.com/samcharles93/ai-sdk to v0.1.29

## [1.18.0] - 2026-08-25

- Fix concurrent chat-turn claims so SQLite writer contention no longer leaks as `SQLITE_BUSY`.
- Make task timelines operationally useful with stage outcomes, durations, retry context, and durable per-stage token economics.
- Return canonical repository, issue, and pull-request URLs for each task's owning GitHub or Gitea identity.
- Preserve per-identity commit attribution and real model context limits across daemon-to-worker dispatch.

## [1.17.1] - 2026-08-25

- fix(update): remove source checkout dependency
- fix(gateway): make UUIDv7 session references unambiguous

## [1.17.0] - 2026-08-25

- fix(webui): add nil receiver guard to Server.trustForwardedHeaders
- fix(chat): restore transcript and update replay

## [1.16.0] - 2026-08-25

**The agent can hand you a file, and the dashboard works behind a reverse
proxy.** Media delivery was fetch-by-URL only, so a file the agent produced
locally — a transcript, a log dump, a screenshot — was passed to Telegram as
a URL it could not fetch: the send did nothing and reported success. The new
`send_file` tool plus upload-by-reader delivery closes that, and every layer
that cannot deliver now says so rather than staying silent. Dashboard
mutations behind a TLS-terminating proxy were refused as cross-origin;
forwarded headers are now honoured, but only under an explicit opt-in that is
off by default.

- feat(telegram): deliver a local file as an uploaded attachment, choosing
  upload-by-reader or fetch-by-URL per attachment; `send_file` resolves paths
  under the same confinement policy the read tool applies, so sending cannot
  reach what reading cannot
- feat(gateway): `task_action` chat tool for operator task management —
  abandon, archive, retry, stop, cancel, approve and reject from chat, on the
  same action path and state machine as the dashboard
- fix(webui): validate mutation `Origin` against the effective external
  scheme and host. `X-Forwarded-Proto`/`-Host` are honoured only when
  `web.trust_forwarded_headers` is set, which is off by default and safe only
  behind a proxy that overwrites or strips untrusted forwarded headers
- fix(webui): report a local file the dashboard chat cannot upload as
  undelivered instead of dropping the event, which had reproduced the exact
  silent non-delivery `send_file` was built to end
- fix(daemon): detach terminal state transitions and cleanup from a cancelled
  context, so a stopped task still records its outcome

## [1.15.0] - 2026-08-24

**Autonomous workflows now have exactly one production execution path.** Every
task is handed complete to a scoped `archie-agent` container over core NATS;
`archied` no longer runs an agent loop in-process. Embedded NATS is the
default and generates a per-start token, binding to the resolved Docker bridge
so managed workers can reach it; external NATS changes only broker placement.
The legacy `[agent]` section and `containers.enabled` still decode for
migration but can no longer select an execution path, and a reloaded
`nats.url` is logged as requiring a restart. Operators upgrading from a
`agent.mode = inprocess` configuration get the managed-worker topology
automatically; see `deployments/` for the supported profiles.

- feat(nats): expose the embedded broker to managed task containers behind a
  generated per-start credential
- fix(daemon): park before worktree access when the container pool or task
  transport is unavailable, rather than silently executing locally
- refactor(agentexec): remove the in-process, subprocess and single-stage
  execution paths
- fix(worktree): report a successful push as successful even when writing
  local upstream tracking metadata afterwards fails, so a published branch no
  longer parks the task as failed
- fix(worktree): honour context cancellation in `CommitAll`, so a cancelled
  task can no longer stage or commit
- fix(daemon): remove the worktree when a task completes with no changes,
  which previously leaked a full clone per no-change task; parked worktrees
  are still retained for post-mortems
- fix(worktree): derive the branch prefix from the issue title before falling
  back to labels, matching the documented contract
- fix(worktree): clean untracked files when `.git` is a gitdir file
- fix(agentexec): recover agents that end a turn without the required finish
  call
- feat(scheduling): ticker engine with a configurable interval and pools
- fix(telegram): keep tool-call context when compacting tool output (#609)
- feat(workflow): add the adversarial-review findings contract, where only a
  confirmed finding blocks and "review found nothing" is distinct from
  "review did not run" (contract only; no review stage runs yet)

## [1.14.0] - 2026-08-23

- fix(telegram): compact tool output and drop turn caps
- fix(update): install only approved components

## [1.13.0] - 2026-08-23

- fix(telegram): render tool progress as bounded code blocks

## [1.12.0] - 2026-08-23

- fix(webui): give dashboard-initiated updates a phase-2 report
- feat(agent): stamp archie-agent's build and report it back to the daemon
- feat(releaseupdate): enrich components with install type and reference
- feat(webui): surface per-component update status on /api/version and Configuration

## [1.11.1] - 2026-08-23

- fix(update): wire Telegram personas and survive callback cancellation
- fix(webui): theme the Workflows 'Start work' form controls

## [1.11.0] - 2026-08-23

- feat(forge): make webhook intake observable and deduplicate concurrent delivery
- fix(telegram): preserve code fences across message splits
- fix(gateway): replay completed-duplicate turns with their tool activity
- feat(storage): persist agent project state across sessions
- fix(container): persist npm cache for per-task MCP servers
- fix(worktree): bind publication to dispatch grants, reset retries to a pristine base, and reject diffs without a merge base
- feat(media): deliver generated media through Telegram with link fallback in the dashboard
- feat(tools): generate videos through MiniMax, gated by provider configuration

## [1.10.0] - 2026-08-22

- feat(forge): GitHub webhook receiver for immediate issue dispatch
- feat(webhookguard): shared HMAC, rate-limit, and redaction mechanics
- feat(nats): run an embedded server when no external NATS is configured
- feat(capture): store unbound webhook events, retention-bounded
- feat(webui): event inspector -- see captured payloads in the dashboard
- feat(webui): payload field mapping -- bind JSON paths to named fields, preview before saving
- fix(webui): stop the capture live-event notification duplicating payloads into the unbounded events table
- fix(update): verify an update took effect instead of trusting the report (#530)
- fix(daemon): refuse to poll on an empty label-trigger match (#529)
- fix(gateway): stop persona from asserting unverified claims as fact (#528)
- fix(gateway): stop ClaimTurn's insert-race branch losing its own retry read
- fix: restore provider gate and dashboard token accounting
- fix: address simple findings from a repo-wide golangci-lint sweep

## [1.9.11] - 2026-08-19

- fix(mcp): persist npx package cache across daemon restarts

## [1.9.10] - 2026-08-19

- feat(update): `/update` now streams live progress while an update installs, reports a clear version summary (previous/installed, daemon/agent) once the build succeeds, and automatically rolls back and reports the outcome if the restarted daemon fails its post-update health check

## [1.9.9] - 2026-08-18

- fix(telegram): drain live renderer in concurrency test

## [1.9.8] - 2026-08-17

- chore: no user-facing changes

## [1.9.7] - 2026-08-17

- chore: no user-facing changes

## [1.9.6] - 2026-08-16

- fix(update): stamp install type and bound retry backoff

## [1.9.5] - 2026-08-16

- refactor(archied): decompose the daemon bootstrap into phased wiring (no behaviour change; splits the ~950-line run() into focused setup phases so complexity linting passes)
- refactor(archied): extract the config overlay boot helper and thread context through chat gateway setup
- refactor(tools): split the read executor and flatten nested config/tool fallback logic to lower complexity
- fix(tools): avoid predeclared `max` names
- fix(email): use context-aware test networking
- fix(indexing): remove the unused code search test runner
- fix(tests): use contexts and type assertions in overlay and gateway tests
- docs(architecture): document the memory engine family

## [1.9.4] - 2026-08-15

- feat(gateway): polish /status command response format
- fix(telegram): preserve URL boundaries in responses

## [1.9.3] - 2026-08-15

- fix(webui): scroll selected command into view on keyboard navigation
- fix(logging): resolve error attrs to their message string, not {}
- feat(agentexec,workflow): surface tool calls on the task timeline

## [1.9.2] - 2026-08-15

- fix(worktree): force checkout so retry recovers from a dirty worktree

## [1.9.1] - 2026-08-15

- fix(agentworker): forward workflow events across the NATS worker boundary
- fix(webui): filter turn_completed noise from the Live Activity SSE feed
- feat(releaseupdate): stamp install type at build time, fail closed on unknown

## [1.9.0] - 2026-08-15

- fix(webui): style tool-call failure from a structured field, not a string prefix
- fix(config,webui): make show_tool_calls one cross-channel setting
- fix(telegram): guarantee no message is stranded on gateway stop/restart
- fix(telegram): use NewRequestWithContext to satisfy noctx
- refactor(webui): decompose handleSSE to cut cognitive complexity

## [1.8.0] - 2026-08-15

- feat(archied): add -version so the installed build can be read from a shell
- fix(webui): rebuild the Configuration page layout so values stop colliding
- fix(webui): size Configuration key/value lists to their own content
- fix(webui): keep Configuration rows full width, hug only the label column
- fix(webui): cap and centre the Configuration page instead of its lists
- fix(webui): preserve encoded redirect path characters
- feat(chat): show tool calls inline and stream replies into one message
- fix(chat): harden inline tool call streaming
- refactor(agent): move worker runtime into application layer
- fix(telegram): harden live-reply delivery against /stop and multi-byte content
- fix(telegram): keep tool activity visible and mark failed/empty turns
- fix(config): de-duplicate disabled-forge predicate, export ForgeDisabled

## [1.7.0] - 2026-08-10

- fix(build): install ripgrep where grep.go actually needs it on PATH
- fix(webui): Send button no longer passes its click event to sendMessage
- feat(webui): Enter sends, Shift+Enter inserts a newline
- fix(chat): stop showing raw 501 banner for unconfigured update-check
- fix(chat): give chat page a real height budget instead of a floor
- fix(workflow): carry gate output into the baseline park error
- feat(logging): add a per-task log sink
- feat(agentexec): publish agent-side logs to the task's system subject
- feat(daemon): consume agent system logs into per-task sinks and the dashboard feed
- feat(webui,gateway): add /api/tasks/{id}/logs endpoint and task_logs chat tool
- refactor(gateway): persist conversations in sqlite
- refactor(store): replace legacy persistence with SQLite
- fix(webui): tighten configuration KV row spacing
- fix(gateway): harden session recovery paths
- refactor(daemon,webui): make Daemon.Cfg and webui.Server.Cfg reload-safe
- feat(config): SIGHUP-triggered reload with requires-restart warnings
- fix(config): share one config Holder between daemon and dashboard; re-apply model catalog on reload
- fix(config): complete the reloadable allowlist and pin it against ForTask
- feat(config): runtime config overlay store, PATCH /api/config, and the `--no-config-overlay` recovery flag
- feat(webui): inline config editing on the Configuration page
- feat(config): surface overlay state, shadowed keys, and per-row reset
- fix(config): close review blocker on the PATCH path; deep-copy snapshots; atomics for shared state
- fix(config): preserve nil slices in Clone to avoid spurious reload warnings

## [Unreleased]

## [1.6.0] - 2026-08-09

- feat(gateway): tool-approval gate, session tools for chat, and /delete command
- fix(webui): close open-redirect in dashboard token-exchange handler
- feat(gateway): make chat turns durable and idempotent
- feat(webui): lifecycle-safe task controls with atomic retry and archive
- feat(config,channels): format-neutral config resolution and truthful channel state
- feat(logging,workflow): daemon log feed and executable workflow registry
- fix(store,gateway): taskstate constants and mobile jump-to search
- feat(webui,gateway): wire config, channel, log-feed, and work-intake seams
- fix(ui): hide inactive chat command menu

## [1.5.0] - 2026-08-08

- feat(curator): engine family contract, registry, and runtime
- feat(curator): trigger accounting — primary-input wake path
- feat(setup): interactive setup steps with a TTY-gated prompter
- feat(webui): rebuild the dashboard and add chat
- feat(daemon): identity-scoped isolation plus gateway session titles and /delete
- feat(logging): optional rotating log file
- feat(configuration): export Validate for the setup flow
- fix(gateway): durable message identity with a SQLite store
- fix(gateway): consistent message ordering and paged search
- fix(gateway): stop /compress destroying history it should have kept
- fix(gateway): do not answer a redelivered message twice
- fix(gateway): claim the session slot before writing it, not after
- fix(gateway): keep /topic off from poisoning the session cache
- fix(gateway): resolve the most recently active session, not an arbitrary one
- fix(gateway): give branch-inherited messages their own identity
- fix(gateway): make both backends agree on newest-first
- fix(store): honour the transition guard in production persistence
- fix(store): move ID counters away from the reserved metadata namespace
- fix(store): surface task timestamps and tag the summary structs
- fix(telegram): pause the update loop when rate limited
- fix(memory): fail startup on an unusable memory directory
- fix(tools): let an optional provider fail without taking the daemon down
- fix(tools): report a cancelled find walk as an error, not empty results
- fix(worktree): name the missing forge credential when a push fails
- fix(tomlwrite): recognise table headers carrying a trailing comment
- fix: close the duplicate-work loop and the dead paths around it
- perf(tools): drop the embedded ripgrep binaries
- refactor: one definition of what approving or declining a task means
- refactor: move workflow into its own domain

## [1.4.1] - 2026-07-31

- fix(catalog): match SDK provider fallback

## [1.4.0] - 2026-07-31

- fix(secrets): wire provider credentials through engines

## [1.3.1] - 2026-07-31

- fix(telegram): omit empty model picker markup

## [1.3.0] - 2026-07-31

- fix(memory): replace framework-specific names with generic equivalents
- feat: unify provider and model selection

## [1.2.0] - 2026-07-31

- feat: add MCP client capabilities to archied and archie-agent
- fix: handleSummary ignores errors from WorkflowStats/StageStats/TokensByDay
- feat: establish Archie identity and use stable callback tokens
- refactor(telegram): keep one model selector command
- fix: handleSSE silently swallows EventsSince backlog fetch errors
- fix: Transition() ignores `from` status guard, allowing lost updates
- fix: skillscript.Run ignores context, cannot be canceled/timed out
- feat(telegram): report installed component versions
- feat(telegram): add approved component updates
- fix: LoadDir: no timeout around Yaegi Eval can hang daemon startup
- feat(chat): real system prompt for Archie — identity, tools, env
- feat(indexing): port tau's codesearch workspace index
- fix(indexing): surface index config properly and stop silent degradation
- refactor(worktree): replace git shell-out with go-git v6
- fix(worktreerpc): bound handler contexts and drop the git binary from tests
- refactor(nats): rebuild as infrastructure/eventbus/nats with real boundaries
- refactor: route tasks on a typed kind, not forge labels
- fix(natsrpc): make RegisterAll wait for the server to see subscriptions
- refactor(config): move loading into infrastructure/configuration
- fix(telegram): publish all executable session commands in menu and help
- fix(telegram): publish the restored gateway commands
- refactor: add internal/eventbus as the broker-neutral messaging contract
- fix(chat): operator name from config, session wiring, prompt rules
- feat(tools): lift tau's file and shell tools into the tool registry
- feat: make /stop cancel the running chat turn
- fix: kill the whole process group when a shell command is cancelled
- feat: refuse unrecoverable shell commands
- feat: make /stop reach running agent tasks
- fix(chat): temper prompt brevity rules with warmth
- fix: use errors.Is/As for context errors and cleanup
- fix(grep): distinguish ripgrep exit 1 from a failed search
- fix(archied): disable the forge on a missing credential, not the daemon
- fix: restore /model parsing and close the 6d4e0be autofix audit
- feat: embed config.example.toml and add comment-preserving TOML writer
- fix(docker): install unzip and Node 24 so MCP servers can start
- fix(docker): slim both images and repair unreachable Go tooling
- fix(container): write the task boot brief under .git
- fix(telegram): drop pending updates before long polling

## [1.1.0] - 2026-07-27

- feat: add the Telegram gateway with persistent sessions, topic-thread support, streamed rich replies, typing indicators, and Markdown rendering
- feat: add `/spawn`, `/approve`, and `/cancel` for chat-driven task control
- feat: provide a comprehensive `/help` guide whose command list stays aligned with Telegram's published menu and Archie's executable command surface
- feat: add `/provider` and provider-filtered `/model` selectors that switch the provider and model used by subsequent live chat turns
- feat: include the active provider and full model reference in `/status`
- feat: notify authorized Telegram users once when a newly tagged component release is installed
- feat: add `/restart` to reload and relaunch the Telegram gateway without interrupting in-flight daemon tasks
- feat: add deny-by-default Telegram sender authorization and native command-menu publication
- feat: connect Desktop Commander through Archie's managed MCP client so chat can use workspace file and process tools
- feat: add multi-identity daemon configuration, persona routing, an email channel, and webhook platform support
- feat: add durable task, session, message, and memory persistence
- feat: add memory synchronization, prefetch, context scrubbing, threat scanning, and built-in memory tools
- feat: add tool guardrails, classification, budgets, availability filtering, and disclosure controls
- feat: add feature-based YAML configuration with `conf.d` overlay loading
- feat: add typed secret-engine registration and Yaegi extension support
- fix: reject unknown slash commands locally instead of allowing the LLM to fabricate command behavior
- fix: publish Telegram commands to default, private-chat, and group-chat scopes so stale commands inherited with the bot token cannot shadow Archie's menu
- fix: drain the SDK's full event stream so streamed Telegram replies cannot deadlock
- fix: use standard newline-delimited JSON framing for MCP stdio clients
- fix: make MCP stdio shutdown and timeout handling race-safe
- fix: restore legacy `[forge].token_env` compatibility and required container environment passthrough
- fix: make the agent-container Docker network explicitly configurable

## [1.0.0] - 2026-07-22

- feat: ship the resident forge-polling daemon, deterministic workflow engine, isolated worktrees, quality gates, Gitea integration, Docker/NATS worker split, plugin loading, and skills support
- feat: publish `archied` and `archie-agent` container images to the Gitea registry
