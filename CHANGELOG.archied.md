# archied changelog

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
- fix(nell): honour the transition guard on the backend that ships
- fix(nell): move ID counters off the SDK-reserved meta: prefix
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
- feat: add NellDB-backed task, session, message, and memory persistence
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
