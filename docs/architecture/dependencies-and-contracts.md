# Dependencies and Contracts

**Status:** Approved foundation  
**Date:** 2026-07-28  
**Beads issue:** `archie-core-5d7`

## Dependency direction

```text
domain and shared application contracts  <-  infrastructure implementations
                    ^
                    |
          application composition
```

The following rules are strict:

1. Domains MUST NOT import `internal/infrastructure`.
2. A domain defines the smallest interfaces required to perform its work.
3. Infrastructure implements those interfaces and translates external
   representations at the boundary.
4. Only application composition chooses concrete implementations.
5. A domain MUST NOT manipulate another domain's state or call its internal
   services.
6. Cross-domain work uses an explicitly designed command, event, or approved
   shared contract.
7. Implementation difficulty is not permission to violate a boundary.

Broker messages, database rows, SDK response types, and configuration DTOs MUST
NOT leak into domain models.

## Commands and events

- An event records something that happened and is owned by its publishing
  domain.
- A command requests an action and is owned by the domain expected to handle it.
- Consumers do not redefine an event's meaning.
- Senders obey the receiving domain's command contract.
- There is no generic package containing unrelated application messages.

## Event bus

`internal/eventbus` is an approved shared application contract. It defines only
the messaging behaviour Archie requires, including publication, subscription,
delivery outcomes, and required lifecycle semantics.

It MUST remain broker-neutral. NATS subjects, JetStream configuration, consumer
groups, acknowledgements, reconnection, and serialization belong under
`internal/infrastructure/eventbus/nats` unless Archie explicitly requires a
semantic guarantee in the shared contract.

Message schemas stay with their owning domains. The event bus transports
messages; it does not own their meaning.

## Wire contracts and generation

Canonical worker and service wire contracts and their authoritative generation
inputs are owned by the domain or capability that defines their meaning. They
MUST NOT be centralized in `internal/infrastructure/spec` or another generic
schema package.

Infrastructure owns:

- generic contract-generation machinery;
- transport-specific adapters and generated implementations;
- RPC, REST, NATS, and other external encoding and delivery concerns.

For example, agent-worker requests and results belong to task execution.
Subprocess, NATS, or HTTP representations are infrastructure implementations of
that contract. The same rule applies independently to forge, persistence,
workspace, and other service contracts.
