# archied changelog

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
