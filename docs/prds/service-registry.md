# Service registry — one declaration per service

**Status:** Design, pre-implementation. Supersedes `state-store-contract.md` §10's
`State ServiceConnection` struct-field instruction (revised to rev. 2d in the same change).

**Base:** the `listen` field, `internal/app/archied/listen.go`, and the listen defaults cited
below exist on `feat/attempt-attribution`, not on `main`. This design therefore lands on top of
that branch: `RegisterService`'s `listen` argument has nothing to carry without it.

## Problem

A service is already one Go type, `config.ServiceConnection`. What is duplicated is its
**name**, in three layers:

| Layer | Location | What is hard-coded |
|---|---|---|
| Declaration | `internal/config/services.go` | `Gateway` and `State` as struct fields |
| Defaulting | `internal/infrastructure/configuration/defaults.go` | three `if cfg.Services.X.Y == ""` branches |
| Consumption | `internal/app/archied/{bootstrap.go,state_store.go,gateway.go}`, `internal/app/archieui/config.go` | `cfg.Services.State` / `cfg.Services.Gateway` field access |

Adding a service means editing all three. The drift this permits is already visible:
`stateStoreResolvedToken` (`state_store_client.go`) and `gatewayResolvedToken`
(`chat_service.go`) are byte-identical apart from the env var name, and
`gateway.Target` is defaulted while `state.Target` is a startup error, with nothing
stating that asymmetry in one place.

`resolveServiceListen(service, flagValue, configured)` (`listen.go`) is already
name-parameterised. It is the shape the rest should follow.

## Decision

**Key services by name, and carry per-service asymmetry as registration data.**

### 1. Declaration collapses to a map

```go
// internal/config/services.go
type Services map[string]ServiceConnection
```

`ServiceConnection` is unchanged. The TOML shape is unchanged, so no deployment profile,
`config.example.toml`, or `install.sh` edit is required. An unrecognised `[services.foo]`
decodes without a code edit, satisfying criterion 5.

### 2. Registration is the single entry point

```go
// internal/config/servicespec.go
func RegisterService(context, name, target, listen, tokenEnv string)
```

registered once per service, at package init in `internal/config`:

```go
RegisterService("both",   "gateway", "127.0.0.1:8585", "127.0.0.1:8585", "GATEWAY_TOKEN")
RegisterService("client", "state",   "",               "127.0.0.1:9090", "STATE_STORE_TOKEN")
```

Positional, five strings, as the story specifies. Each argument is data the current code
states in prose or in a branch:

- `context` is `client`, `server`, or `both`, and decides which fields must resolve
  non-empty. `RegisterService` enforces it: an unknown context, or a hosted service with
  no default `listen`, panics at registration. Registrations come from this repository's
  own `init()`, never from operator input, so an invalid one is a programming error with
  no caller positioned to handle an error return. A documented-but-unread `Context` would
  be worse than no field at all.
- `target` is the **default**, and an empty default means the operator must supply one.
  That single convention replaces the `state.target` required / `gateway.target` defaulted
  asymmetry with no branch: `state` registers `""`, `gateway` registers an address.
- `listen` is the default listen address, feeding the existing
  flag → `[services.<name>].listen` → default precedence unchanged.
- `tokenEnv` names the fallback env var, replacing the two copied resolvers with one.

### 3. Defaulting and consumption become loops and lookups

`applyDefaults` iterates the registry instead of naming services. Consumers read
`cfg.Services.Get("state")`, returning a `ServiceConnection` plus the spec, so
`stateStoreResolvedToken` and `gatewayResolvedToken` collapse into
`Services.ResolvedToken(name, secrets)`.

## Criteria satisfied

1. Declared once, in the `RegisterService` call.
2. No second location can contradict it; the map has no per-service fields to disagree with.
3. Registration is the entry point, with the five fields a service has.
4. A new service is one `RegisterService` line: no field, no default branch, no consumer edit.
5. The set of services is registry data, not a struct definition.

## Scope and risk

- `state-store-contract.md` §10 must be revised in the same change, or the tree contradicts
  its own ratified authority. Its summary (`:7`) already says `[services.<name>]`.
- A typo'd `[services.gatway]` is **reported today** (`UnknownKeys = [services.gatway,
  services.gatway.target]`), because the struct form leaves it in `toml.MetaData.Undecoded()`.
  A map decodes any section, so preserving that is a requirement, not an optional extra:
  `unregisteredServiceKeys` asks the registry which names exist. This costs nothing in
  extensibility, because the answer comes from registration data rather than a fixed list.
  (An earlier draft of this document claimed the typo was silently ignored either way. It
  was not; the behaviour was measured.)
- Existing tests reference `cfg.Services.State` directly (`state_store_client_test.go`,
  `setup_test.go`, and others). They move to `Get`, mechanically.
- No wire contract, no RPC, and no persisted format is touched. The 42-RPC State Store
  contract is unaffected.

## Acceptance

- Adding a third service is a one-line diff plus its consumer, proven by a test that
  registers a throwaway service and asserts its defaults and token fallback resolve.
- `grep -rn 'Services\.\(State\|Gateway\)' --include='*.go'` returns nothing.
- The existing `services_test.go` default and precedence assertions pass unchanged in
  behaviour, rewritten only to the new accessor.
