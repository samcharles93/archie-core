# archie-agent changelog

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
