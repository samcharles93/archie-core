# Messaging Service boundary and transport design

**Status:** Approved
**Revision:** 1  
**Date:** 2026-09-19  
**Beads issue:** `archie-core-8cda.6.1`  
**Parent:** [`service-decomposition.md`](service-decomposition.md)  
**Prerequisites:** Gateway contract extraction (Phase 1), State Store extraction (Phase 2), UI extraction (Phase 3)

This document is the authoritative specification for Phase 4: Messaging Service extraction. It fixes the process boundary, configuration ownership, client contracts, security posture, readiness behaviour, channel invariants, and deletion criteria required by `archie-core-8cda.6` and its child tasks.

## Decision

The Messaging Service (`cmd/archie-messaging`, composed via `internal/app/archiemessaging`) owns external channel connections (Telegram, email, webhook) and dispatches inbound messages and commands to the standalone Gateway Service over gRPC via `messaging.ChatContract`.

It is a peer service to the UI Service and the daemon:

- It serves external chat participants across configured communication platforms.
- It consumes Gateway's `messaging.ChatContract` exclusively for chat turn execution, streaming, session queries, cancellation, persona selection, and task control actions.
- It holds no direct database connections, owns no LLM execution or model provider credentials, and has no dependency on the daemon runtime or the UI HTTP server.

## Boundary and Ownership

| Concern                | Messaging Service owns                                                          | Messaging Service consumes                                         | Messaging Service must not own or link                             |
| ---------------------- | ------------------------------------------------------------------------------- | ------------------------------------------------------------------ | ------------------------------------------------------------------ |
| **Channel transports** | Telegram bot long-polling, Email IMAP/SMTP loops, Inbound Webhook HTTP listener | Gateway `ChatContract`                                             | Direct LLM providers, model runtimes, tokenizer libraries          |
| **Turn dispatch**      | Inbound message packaging, typing indicators, live streaming updates            | `ChatContract.Route`, `ChatContract.Stream`                        | `gateway.Router`, `runtime.Runtime`, agent prompt builders         |
| **Sessions & History** | Platform-to-session key mapping, conversation thread ID translation             | `ChatContract.GetSession`, `RecentMessages`, `RecentTurns`         | store implementations, `SessionStore` database handles             |
| **Control & Actions**  | Platform command parsing (`/status`, `/tasks`, `/cancel`, `/stop`, `/persona`)  | `ChatContract.Snapshot`, `ApplyTaskAction`, `Cancel`, `SetPersona` | `daemon.Daemon`, workflow execution engine, worktree operations    |
| **Task Approvals**     | Channel interactive keyboards, approval callbacks                               | `ChatContract.ApplyTaskAction`                                     | Direct task status mutations, forge comment posters                |
| **Configuration**      | Platform bot tokens, allowed sender/user IDs, webhook listen ports              | `[chat.*]` channel configs, Gateway target/token                   | `config.Holder`, forge tokens, repo configs, agent container specs |
| **Readiness**          | Messaging process liveness, channel connectivity, Gateway probe                 | Gateway `ChatContract.Snapshot`                                    | Internal daemon subsystem probes                                   |

## Target Service Shape

1. **Binary entry point**: `cmd/archie-messaging/main.go` parses CLI flags (`-config`, `-config-overlay`, `-gateway-target`, `-gateway-token`) and hands execution to `internal/app/archiemessaging.Run`.
2. **Application composition**: `internal/app/archiemessaging/` wires:
   - Config loading scoped strictly to `[chat.*]` and `[services.gateway]`.
   - Secret resolution for channel credentials (e.g. Telegram bot token).
   - Gateway gRPC client (`internal/infrastructure/gatewayrpc.Client`).
   - Channel lifecycle managers (`internal/channels/status`).
   - Channel instances (`internal/channels/telegram`, `email`, `webhook`).
3. **Turn and Stream Execution**:
   - Channels adapt inbound platform messages to `messaging.Inbound`.
   - Streaming channels (Telegram) consume `ChatContract.Stream(ctx, inbound)` which returns `<-chan messaging.ChatEvent`, mapping `started`, `delta`, `tool`, `media`, `done`, and `error` events directly to live message frames.
   - Non-streaming channels (email, synchronous webhooks) consume `ChatContract.Route(ctx, inbound)`.

## Load-Bearing Channel Invariants

The Messaging Service preserves existing invariants defined in `AGENTS.md` and channel documentation:

1. **Telegram long-polling**: Must call `dropPendingUpdates(ctx, b)` before `b.Start(ctx)` and on `/restart`.
2. **Telegram command menus**: Must register command menus simultaneously to `default`, `all_private_chats`, and `all_group_chats` via `commands.go` to prevent menu shadowing.
3. **No UI detour**: Turns and task actions route directly to Gateway's `ChatContract`; no HTTP request may ever be sent to `archie-ui` or `internal/webui`.
4. **Sender Confinement**: Telegram sender checks against `allowed_user_ids` must continue to fail closed before initiating any turn dispatch.

## Configuration Ownership

`cmd/archie-messaging` reads only:

- `[services.gateway].target` and `[services.gateway].target_token` (or CLI flags).
- `[chat.telegram]` (token, allowed user IDs).
- `[chat.email]` (IMAP/SMTP host, ports, credentials).
- `[chat.webhook]` (listen address, secret).
- `[secrets]` (for resolving token secret references).

It does not read or decode `[forge]`, `[repos]`, `[models]`, `[providers]`, `[runners]`, `[containers]`, or `[nats]`.

## Deletion Gate and Verification

The extraction is guarded by `cmd/archie-messaging/architecture_test.go`, verifying that `go list -deps ./cmd/archie-messaging` links zero banned runtime packages:

- `modernc.org/sqlite`, `github.com/jackc/pgx`, `internal/infrastructure/postgres` and `internal/infrastructure/legacyread` (State Store)
- `internal/daemon`, `internal/app/archied`, `internal/container`, `internal/worktree` (Daemon runtime)
- `internal/domain/workflow`, `internal/agentexec`, `internal/taskrun` (Workflow engine)
- `internal/forge`, `internal/forgerpc` (Forge client)
- `internal/gateway` runtime (only `internal/domain/messaging` and `internal/infrastructure/gatewayrpc` are permitted)
- `internal/webui` (Dashboard)
