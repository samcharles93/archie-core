# archied changelog

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
