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

1. Add it to the struct the resource's `Document` names in
   `internal/app/controlplane/operational_settings.go`. The resource's JSON
   schema and the dashboard's Advanced settings editor are generated from that
   type, so no UI work is needed for the editor itself.
2. Stored documents use Go field names as keys (`Image`, `MaxConcurrency`).
   Give the field no `json` tag unless its neighbours have one.
3. A map or a nested struct needs its layering checked. `json.Unmarshal`
   merges into an existing map, so a stored map would add to the file's rather
   than replace it. A field the stored document omits keeps the file's value
   (a document stored before the field existed). Add a case to
   `internal/app/controlplane/runtime_config_test.go` for both.
4. Validate it in one function both sides call, and add a case to
   `TestResourceValidatorsRejectWhatEffectiveValidationRejects` in
   `validators_agree_test.go`, so the file and the resource cannot disagree.
5. Decide whether it reloads: it does only if every consumer re-reads it from
   `config.Holder`. The allowlist is `internal/app/archied/reload.go`; anything
   not on it requires a restart.
6. If the settings page shows it as a plain field, add it to
   `internal/webui/config_schema.go` and the view in `api_config.go`.

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
