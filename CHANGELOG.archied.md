# archied changelog

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
