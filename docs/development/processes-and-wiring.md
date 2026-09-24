# Processes and wiring

Authority: `docs/architecture/organisation.md` (layers, process boundaries),
`docs/architecture/dependencies-and-contracts.md` (dependency direction, wire
contracts) and `docs/architecture/plugins-and-extensions.md` (the plugin engine
rule).

## Placing new code

- Domain logic, entities and the interfaces it needs go in
  `internal/domain/<area>/`, which imports no infrastructure or app package.
- Implementations of those interfaces go in `internal/infrastructure/<service>/`.
- Construction and wiring go in `internal/app/<application>/`; `cmd/` only
  parses flags and calls it.
- `logging`, `events`, `eventbus`, `policy` and `taskstate` import no internal
  package. There is no `shared/`, `utils/` or `common/` package.
- A behaviour has one authoritative path. Before adding a second way to do
  something, remove or redirect the first.

## Adding a capability to a process

1. Decide which process owns it. A new binary is justified only when the
   surface must fail, scale or run somewhere independently.
2. Wire it at that process's composition root. An optional capability
   degrades: a missing provider logs a warning and marks health degraded
   rather than stopping the process (MCP providers are the model).
3. Every process that reaches the capability through the State Store gets it
   through a `storecontract` interface asserted on the client
   ([State Store and persistence](state-store-and-persistence.md)).
4. The architecture tests (`cmd/archie-ui/architecture_test.go`,
   `cmd/archie-messaging/architecture_test.go`, `TestOpenStoresNeverOwnsTaskDB`)
   pin what each binary may link. A failure there means the placement is wrong,
   not the test.

## Adding a plugin engine

Satisfy every invariant in "Plugin engine rule (strict)" in
`plugins-and-extensions.md`: typed domain contract, an owning manager,
lifecycle and health, narrow host access, and an explicit trust boundary.
`internal/plugin/architecture_test.go` checks the parts that can be checked.

## Adding a channel or a NATS subject

- Channel rules (Telegram's pending-update drop, command scopes) are in
  `CLAUDE.md` under Per-Package Invariants.
- A subject is part of the wire contract. Its payload type belongs to the
  domain that defines its meaning, and the transport code lives in
  infrastructure.
