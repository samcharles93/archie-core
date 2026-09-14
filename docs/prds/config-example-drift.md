# Example-config and generated-config drift — decision

**Status:** Decided, not yet implemented
**Date:** 2026-09-15
**Closes:** the checked-in-config half of audit findings `config-1` and
`repo-root-deployment-2`; strengthens `doc-drift` findings generally.
**Authority:** extends `docs/architecture/generated-documentation.md` and
`docs/prds/config-schema.md`; does not replace either.

## Question

Should `config.example.toml` and `deployments/*.toml` be *generated*, so they
cannot drift from the config code?

## Decision

**No — do not generate them. Gate them instead.** Neither file is a projection
of the schema, and generating either would destroy the thing that makes it
useful. Three verified reasons, in order of weight:

1. **`config.example.toml`'s value is its comments.** It documents **137 keys**
   (measured: active *and* commented-out), and those hand-written comments are
   the file's purpose — `docs/architecture/configuration.md` states outright
   that "`config.example.toml` carries documentation users are meant to
   hand-edit". TOML round-tripping does not preserve comments, which is exactly
   why `tomlwrite` does line-oriented edits rather than decode/encode. A
   generator would emit a correct file with the documentation deleted.
2. **`docs/prds/config-schema.md` forbids building on `config.Config`.** That PRD
   rejects "a reflection-based schema over `config.Config`" because the struct is
   "scheduled for dissolution". A generator deriving the example from its struct
   tags would build on a foundation this repo has already decided to remove.
3. **`docs/architecture/generated-documentation.md:225` prohibits it.** "Adding
   another hardcoded type list to `docsgen` is prohibited" — so any generator
   must consume a domain-owned registry, which does not exist for configuration
   yet. Creating one is the config-domain migration's job, not this change's.

The deployment profiles are also not a projection: they set **3-46 keys** against
the example's 137, and they are curated overlays ("a complete config, not an
overlay", per their own headers), not near-duplicates. Generating them would add
machinery to remove duplication that largely is not there.

## The gap that is real

**Unknown config keys are silently ignored.** Verified at
`internal/infrastructure/configuration/decode.go:27`:

```go
if _, err := toml.DecodeFile(path, target); err != nil {
```

There is no `MetaData.Undecoded()` check, so a typo — `max_concurrancy = 4` —
parses clean, validates clean, and does nothing. That is the same failure shape
this repo's audit found repeatedly: a setting that looks applied and is not. It
is also detectable **without any hand-maintained list**, because `BurntSushi/toml`
reports exactly which keys the target did not consume.

Note the second, separate gap already admitted in writing:
`generated-documentation.md:331` — "`task check` does not run `docs:check`, so
generated drift is currently ungated."

## What to build

**1. Unknown-key detection at the loader (behaviour).** Decode with metadata and
collect `Undecoded()`. Report it as a warning at startup listing the offending
keys, and expose it so tests can assert emptiness.

**Deliberately a warning, not a fatal error.** `configuration.md`'s settled
startup policy is "a missing credential disables a capability; only an invalid
config stops the daemon". A stray key is far more likely to be an operator typo
in a file they are otherwise happy with than a reason to refuse to boot, and
`[web]` is already documented as landing in a *different* binary
(`internal/app/archieui`), so a key unknown to one process is legitimately known
to another. Failing closed here would break working deployments on upgrade.

**2. A checked-in-config contract test (gate).** Load `config.example.toml` and
every `deployments/*.toml` through the real loader and assert `Undecoded()` is
empty, with an explicit allowlist for keys another binary owns. This is the
mechanism that would have caught a renamed key left behind in an example, and it
needs no inventory.

**3. Gate generated data in `task check`.** Add `docs:check` to the gate, closing
the admitted `generated-documentation.md:331` hole. Cheap, already implemented as
`go -C tools run ./docsgen check`.

**4. Explicitly NOT now: a generated key inventory.** `configuration.md`'s
completion criteria want "generated documentation lists every external key,
default, constraint, mutability class, owner". That is a real target and it is
the config domain's, once a domain-owned registry exists — the prohibition above
means it cannot be bootstrapped from a list inside `docsgen`. Recorded as
deferred, not rejected.

## Non-goals

- No generation of `config.example.toml` or `deployments/*.toml`.
- No reflection-based schema over `config.Config`.
- No new type list inside `docsgen`.
- No change to `archied setup`'s template: `configtemplate.Example` stays the
  verbatim embedded example, hand-maintained, and `setup` continues to *edit* it
  via `tomlwrite` rather than regenerate it.

## A withdrawn premise, recorded so it is not reused

An earlier measurement in this session claimed 17 keys were "set by a profile
but documented by no example", and used it to argue for generation. It was an
artifact: the extractor stripped comment markers and tracked a single table
variable, so commented blocks were mis-attributed. Re-checking refuted it —
`providers.<name>.api_key_env` is a real, documented backwards-compatible field
(`internal/config/config.go:180`, `config.example.toml:101`) and `[[identities]]`
is documented at `config.example.toml:53`. The decision above does not rest on
that measurement.

## Acceptance criteria

1. A config file containing an unknown key surfaces those keys to the operator at
   startup, and the daemon still starts.
2. `config.example.toml` and every `deployments/*.toml` are loaded by a test that
   fails if any key is undecoded, except keys explicitly attributed to another
   binary.
3. `task check` fails when committed generated data is stale.
4. Introducing a renamed key into `config.example.toml` fails the gate (prove it
   by doing it, not by asserting it).
