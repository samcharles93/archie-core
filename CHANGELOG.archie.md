# archie-agent changelog

## [1.17.0] - 2026-08-25

- Compact task history against the selected model's real context capacity without imposing a token cap.
- Preserve completed steps and billed usage when a later provider call fails; failed compaction now keeps the original history and continues.
- Persist detailed agent-stage usage through the daemon-owned store before lossy live event fan-out.
- Apply configured or forge-derived author and committer identity to sandboxed task commits.

## [1.16.1] - 2026-08-25

- fix(agentworker): reconcile task clone ownership after commits

## [1.16.0] - 2026-08-25

**Version realigned with `archied`.** This release jumps from 1.9.9 to 1.16.0
to put the two components on matching numbers; there are no 1.10-1.15 agent
releases. It clears the accumulated runtime backlog in one cut rather than
bundling it as a rider on a later change, so the list below spans several
weeks of work rather than one session's.

Highlights:

- feat(nats): run an embedded server when no external NATS is configured, and
  expose the embedded broker to managed task workers
- feat(deployment): default to managed NATS workers; the daemon requires a
  full-task worker handoff and the legacy in-process, subprocess and
  single-stage execution paths are gone
- feat(forge): GitHub webhook receiver for immediate issue dispatch, with
  dedup regression proof; unbound webhook events are stored retention-bounded
- feat(webui): payload field mapping -- bind JSON paths to named fields and
  preview before saving
- feat(scheduling): ticker engine with configurable interval and pools
- feat(storage): persist agent project state
- feat(agent): stamp archie-agent's build and report it back to the daemon
- feat(tools): `generate_video` via MiniMax, gated behind config, and
  `MultimodalResult.URLs` for remote-hosted media
- feat(workflow): define the adversarial-review findings contract

Fixes:

- fix(worktree): bind publication to dispatch grants; reset retries to a
  pristine base; reject diffs without a merge base; clean untracked files when
  `.git` is a gitdir file; post-push metadata resilience, cancellation and
  branch naming
- fix(container): persist the npm cache for per-task MCP servers
- fix(agentexec): recover omitted finish calls
- fix(agentworker): move JSONL decode into storage, unblocking CI
- fix(telegram): compact tool output and drop turn caps
- fix(webui): give dashboard-initiated updates a phase-2 report; restore the
  provider gate and dashboard token accounting
- refactor: remove dead `BasePlatformAdapter` code and legacy execution paths

Shared with archied 1.16.0 (these packages are in both closures):

- feat(telegram): send local files as uploads, not fetchable URLs
- fix(webui,sendfile): close three holes the adversarial review found
- fix(webui): validate mutation Origin against the effective external scheme

## [1.9.9] - 2026-08-18

- chore: no user-facing changes

## [1.9.8] - 2026-08-17

- chore: no user-facing changes

## [1.9.7] - 2026-08-17

- chore: no user-facing changes

## [1.9.6] - 2026-08-16

- chore: no user-facing changes

## [1.9.5] - 2026-08-16

- refactor: decompose the daemon bootstrap into phased wiring (no behaviour change; complexity-driven restructure)
- refactor(tools): split the read executor and flatten nested config/tool fallback logic to lower complexity
- fix(tools): avoid predeclared `max` names

## [1.9.4] - 2026-08-15

- chore: no user-facing changes

## [1.9.3] - 2026-08-15

- fix(logging): resolve error attrs to their message string, not {}
- fix(agentworker): back off before refetching a nak'd stage message
- feat(agentexec,workflow): surface tool calls on the task timeline

## [1.9.2] - 2026-08-15

- fix(worktree): force checkout so retry recovers from a dirty worktree

## [1.9.1] - 2026-08-15

- fix(agentworker): forward workflow events across the NATS worker boundary

## [1.9.0] - 2026-08-15

- fix(agent): resolve git dubious-ownership in sandbox containers
- feat(agent): mark the mounted worktree as a git safe directory
- fix(agent): route git safe.directory setup through markWorktreeSafe
- feat(chat): show tool calls inline and stream replies into one message
- refactor(agent): move worker runtime into application layer
- fix(config,webui): make show_tool_calls one cross-channel setting

## [1.4.0] - 2026-08-10

- feat(gateway): tool-approval gate, session tools for chat, and /delete command
- feat(gateway): make chat turns durable and idempotent
- feat(webui): lifecycle-safe task controls with atomic retry and archive
- feat(logging,workflow): daemon log feed and executable workflow registry
- fix(store,gateway): taskstate constants and mobile jump-to search
- feat(webui,gateway): wire config, channel, log-feed, and work-intake seams
- fix(build): install ripgrep where grep.go actually needs it on PATH
- fix(workflow): carry gate output into the baseline park error
- feat(logging): add a per-task log sink
- feat(agentexec): publish agent-side logs to the task's system subject
- feat(daemon): consume agent system logs into per-task sinks and the dashboard feed
- feat(webui,gateway): add /api/tasks/{id}/logs endpoint and task_logs chat tool
- refactor(store): replace legacy persistence with SQLite
- refactor(daemon,webui): make Daemon.Cfg and webui.Server.Cfg reload-safe
- feat(config): SIGHUP-triggered reload with requires-restart warnings
- feat(config): surface overlay state, shadowed keys, and per-row reset
- fix(config): close review blocker on the PATCH path; deep-copy snapshots; atomics for shared state
- fix(config): preserve nil slices in Clone to avoid spurious reload warnings

## [1.3.0] - 2026-08-08

- feat(curator): engine family contract, registry, and runtime
- feat(curator): trigger accounting — primary-input wake path
- feat(logging): optional rotating log file
- feat(daemon): identity-scoped isolation
- fix(secrets): wire provider credentials through engines
- fix(store): surface task timestamps and tag the summary structs
- fix(tools): let an optional provider fail without taking the daemon down
- fix(tools): report a cancelled find walk as an error, not empty results
- fix(worktree): name the missing forge credential when a push fails
- fix: close the duplicate-work loop and the dead paths around it
- perf(tools): drop the embedded ripgrep binaries
- refactor: one definition of what approving or declining a task means
- refactor: move workflow into its own domain

## [1.2.0] - 2026-07-31

- feat: add MCP client capabilities to archied and archie-agent
- fix: Transition() ignores `from` status guard, allowing lost updates
- fix: skillscript.Run ignores context, cannot be canceled/timed out
- fix: LoadDir: no timeout around Yaegi Eval can hang daemon startup
- fix(indexing): surface index config properly and stop silent degradation
- refactor(worktree): replace git shell-out with go-git v6
- fix(worktreerpc): bound handler contexts and drop the git binary from tests
- refactor(nats): rebuild as infrastructure/eventbus/nats with real boundaries
- refactor: route tasks on a typed kind, not forge labels
- fix(natsrpc): make RegisterAll wait for the server to see subscriptions
- refactor(config): move loading into infrastructure/configuration
- refactor: add internal/eventbus as the broker-neutral messaging contract
- feat(tools): lift tau's file and shell tools into the tool registry
- feat: make /stop cancel the running chat turn
- fix: kill the whole process group when a shell command is cancelled
- feat: refuse unrecoverable shell commands
- feat: make /stop reach running agent tasks
- fix: use errors.Is/As for context errors and cleanup
- fix(grep): distinguish ripgrep exit 1 from a failed search
- fix(docker): slim both images and repair unreachable Go tooling
- fix(container): write the task boot brief under .git

## [1.1.0] - 2026-07-27

- feat: add streamed agent execution with typed message events
- feat: add MCP client transports and managed tool-provider discovery
- feat: add memory context scrubbing and normalized memory tool schemas
- feat: add plugin metadata extraction from `metadata.archie.plugins`
- feat: add configurable container networking for daemon-launched runtimes
- fix: preserve valid UTF-8 when clipping runtime output
- fix: make MCP stdio lifecycle, retries, and timeout handling race-safe
- fix: enforce a zero-finding lint and vet baseline

## [1.0.0] - 2026-07-22

- feat: ship the isolated agent runtime, NATS RPC boundary, workflow execution, skills, and plugin support
- feat: publish the `archie-agent` container image to the Gitea registry
