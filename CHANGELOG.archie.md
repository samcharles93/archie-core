# archie-agent changelog

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
