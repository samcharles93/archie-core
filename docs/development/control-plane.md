# Control plane and settings

Authority: `docs/architecture/configuration.md`. The wiring matrix, the
decoded-but-unwired ledger and the three shapes an unwired field takes are in
`.agents/skills/archie-config-and-flags`.

A field that parses is not a field that works. Before adding one, name the code
that reads it and the path the value takes to get there.

## Decide what kind of setting it is

- **Bootstrap-only** (read once at process start: `database_url`, `state_dir`,
  `work_dir`). It stays in the file, is checked by `validateBootstrap` in
  `internal/infrastructure/configuration/validate.go`, and the runtime overlay
  refuses changes to it.
- **Database-owned** (repository policies, scheduling, tools, plugins,
  channels, containers). The file only seeds it. Once the State Store holds the
  control-plane resource, the stored document is layered over the file
  (`controlplane.Client.RuntimeConfig`) and the file key stops mattering. It is
  checked by `validateDatabaseOwned` and by the resource's validator.
- **Worker-carried.** A value the agent container needs travels in
  `config.TaskConfig` (`Config.ForTask`) or on `taskrun.Request`. Never pass it
  as an environment variable the daemon did not already define, and never carry
  a secret this way.

## Adding a database-owned field

1. Add it to the wire-shape type the resource's `Document` names — a dedicated
   struct in `internal/app/controlplane/` with explicit snake_case json tags
   (`channel_settings.go`, `tool_settings.go`, `container_settings.go` are the
   precedents), never the internal config struct itself. A struct fed straight
   into the document falls back to Go field names (`Window`, `MaxRequests`),
   and a bare `time.Duration` marshals as nanoseconds — both render unusable
   in the Web UI. The resource's JSON schema and the dashboard's Advanced
   settings editor are generated from that type, so no UI work is needed for
   the editor itself.
2. Map to and from the internal config type at the resource boundary (`Seed`,
   the layering in `runtime_config.go`, and the validator all use the same
   mapping). Durations in the document are a string-marshalling type
   (`config.Duration` or a document-local one), never a bare `time.Duration`.
3. A document stored before the shape existed carries Go-cased keys. Read them
   with `controlplanerpc.DecodeRenamingLegacyKeys` (an `UnmarshalJSON` on the
   wire type) while keeping `DisallowUnknownFields` — dropping unknown keys
   would decode a misspelling as "unset" — and register a `Normalize` on the
   `Definition` that re-encodes the document, so a legacy document converges
   the next time it is written. A map or a nested struct needs its layering
   checked: a stored map replaces the file's, not merges into it, and a field
   the stored document omits keeps the file's value. Add a case to
   `internal/app/controlplane/runtime_config_test.go` for both.
4. Validate it in one function both sides call, and add a case to
   `TestResourceValidatorsRejectWhatEffectiveValidationRejects` in
   `validators_agree_test.go`, so the file and the resource cannot disagree.
5. Decide whether it reloads: it does only if every consumer re-reads it from
   `config.Holder`. The allowlist is `internal/app/archied/reload.go`; anything
   not on it requires a restart.
6. If the settings page shows it as a plain field, add it to
   `internal/webui/config_schema.go` and the view in `api_config.go`.
7. A generic catalog resource (`replace` command, not domain-managed, no
   query service) is watched **once per archie-ui process** by `webui.RunLive`,
   then published to every browser through `/api/stream`. Do not add an
   EventSource per resource to a settings store or a gRPC Watch per HTTP client.
   The control-plane server polls `Resource` every 250ms per upstream Watch;
   verify watch count is independent of client count and that late clients get
   the hub's current value (`internal/webui/live_hub_test.go`, real server over
   bufconn). Reopens resume after the last delivered version and charge failed
   attempts; accepting a gRPC stream does not establish its health.

## Every new field

- Test through the layering: build the effective config the way boot does,
  not by setting the field on the consumer.
- Add a commented example to `config.example.toml`. Touch `deployments/*.toml`
  only when a profile there needs a value different from the default.
- Update the wiring matrix in `.agents/skills/archie-config-and-flags`.
- Secrets are `SecretRef`s resolved through the secret registry. Never inspect
  a resolved value, even in a test.
- Reject a configuration that sets a field with no consumer rather than
  accepting and ignoring it (per-identity `forge.intake` is the precedent).
