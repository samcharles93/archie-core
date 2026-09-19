# Self-update: artifact install, topology migration, journaled transaction

Epic: `archie-core-hj1t` / GitHub `#804` ("[EPIC] Self-update: artifact
install, topology migration, journaled transaction").

The update *contract* (`internal/releaseupdate`: `Catalog.Check`,
`Installer.Install`, plus defer/watchdog/verify) is settled and unchanged by
this document — it stays a thin Go interface shelling out to
`CommandCatalog`/`CommandInstaller`. This document decides what the reference
adapter scripts (`scripts/archie-update-{check,install,watchdog}`) and
`install.sh` do behind that contract, and settles the one thing the epic's
own description got wrong: the dist-zip **is** already attached to GitHub
Releases (`.github/workflows/deploy.yml:283-316`, `gh release upload`), not
Actions-artifact-only. Artifact install is therefore a smaller change than
the epic assumed — pointing the installer at an existing asset, not standing
up new CI publishing. Topology migration and the journaled transaction are
genuinely greenfield: no topology-detection or transaction-journal code
exists anywhere in the tree today.

Out of scope: rewriting the `internal/releaseupdate` Go interfaces
themselves, and any UI for triggering updates (channel dispatch, e.g.
Telegram `/update`, is unaffected — this only changes what happens once an
install is triggered).

## Problem

`scripts/archie-update-install:139-160` clones and compiles from source on
every update — the only path that exists. `deploy.yml` already publishes a
per-platform archive plus (implicitly) checksums as a release asset, so the
script not using it is a missed-reuse gap, not a missing-CI gap.
`install.sh:414-445` writes only `archied.service` while every shipped
deployment profile (`deployments/*.toml`) declares `[services.state]` and
`[services.gateway]` sections needing their own units — a fresh install
already reproduces the broken single-unit shape `deployment_contract_test.go`
(lines 70-105) doesn't even catch, because that test only checks profile
syntax, not installed topology. No code anywhere enumerates which systemd
units exist on a host or compares that against what a release requires — the
daemon fails at `bootstrap.go`'s `[services.state].target` check with no
prior diagnosis. The self-refresh trap is confirmed at
`scripts/archie-update-install:195-199`: new adapter copies land on disk but
bash keeps executing the in-memory copy, so a pre-flight check added to the
script only protects the *next* update.

## Design

### Artifact install (`#804` item 1)

**Decision.** `CommandInstaller`'s default script downloads the per-platform
archive plus `SHA256SUMS` from the GitHub Release that `Catalog.Check`
resolved (the same release `deploy.yml` already publishes to), verifies the
checksum, and stages it — it does not clone/compile. Source build
(`scripts/archie-update-install`'s current clone+compile body) is kept as an
opt-in path selected by an explicit `--from-source` flag or
`ARCHIE_UPDATE_SOURCE_BUILD=1` env var, never the default, since a compile-on
every-host update is strictly slower and has a larger failure surface than
downloading a signed asset that CI already produced.

**Call site.** `scripts/archie-update-install` — replace the body currently
at lines 139-160 with a download-and-verify step; move the existing
clone+compile body behind the opt-in flag rather than deleting it.
`internal/releaseupdate/command.go:44-48` (`CommandInstaller`) is unchanged —
it already just shells out.

**Reuse check.** `deploy.yml:265-316` already builds and publishes the
archive and uploads it as both an Actions artifact and a release asset; no
new CI work is needed, only pointing the installer at what exists.

**Testing.** Checksum-verification logic is a shell function, testable with
a fixture archive + `SHA256SUMS` pair — add to whatever test harness already
covers `scripts/`. Full download-from-real-release is a manual smoke command
(document the exact `archie-update-check`/`--dry-run` invocation once built),
not `task check`.

### Topology migration (`#804` item 2)

**Decision.** Add a topology *observer* to the daemon, not the shell
adapter — CLAUDE.md's own "known trap" note says the shell copy is defence
in depth only, and the daemon is what "knows the deployment and holds the
approved snapshot." The observer inspects which systemd user units exist
(`systemctl --user list-unit-files 'archie*.service'` or equivalent) and
which config sections are present (`[services.state]`, `[services.gateway]`,
UI service config), producing an `ObservedTopology` value with one field per
unit: present/absent, and the config sections backing it. Each release's
`release.json` (already produced by `deploy.yml` per the artifact-install
work above) gains a `required_topology` field naming the units and config
sections that release's binaries need. `Catalog.Check` compares observed
against required; when the release needs a unit the host doesn't have, the
result is not an error — it's a `MigrationPlan` value naming exactly which
units and config blocks must be created and in what order (state store →
gateway → UI → daemon, per CLAUDE.md's process boundaries), which the
installer must apply before installing binaries, or refuse with that plan
printed to the operator.

**Call site.** New topology-observation code lives in
`internal/app/archied/` next to `bootstrap.go` (which already owns the
`[services.state]`/`[services.gateway]` checks this reuses), not in
`internal/releaseupdate` — the update package consumes the daemon's
observation, it doesn't reimplement config parsing. `install.sh:414-445`
reads the same `required_topology` declaration (from whichever profile the
operator selected) so first-install and self-update stop diverging — this is
the fix for the "three docs describe four units, install.sh writes one"
gap: one topology source of truth, consumed by both entry points.
`deployment_contract_test.go` gains an assertion that each shipped profile's
declared topology matches the units `install.sh` would create for it.

**Failure mapping.**

| condition | contract error |
|---|---|
| observed topology cannot be determined (systemctl unavailable, non-systemd host) | refuse with "topology unknown" plan naming the manual steps, never guess |
| required units missing, migration plan derivable | apply plan before binary install, in dependency order |
| required units missing, no derivable plan (e.g. conflicting existing unit) | refuse, print the plan, do not proceed |

**Testing.** `ObservedTopology`/`MigrationPlan` comparison logic is pure —
table-driven unit tests belong in `task check`. Actual `systemctl` inspection
needs a real systemd host; manual smoke test only.

### Journaled transaction (`#804` item 3)

**Decision.** Replace `archie-update-install`'s copy-and-trap body with: (1)
pre-flight (existing check at line 170, kept), (2) write an intent journal
to a path outside every directory the install touches — e.g.
`${XDG_STATE_HOME:-~/.local/state}/archie/update.journal` — recording the
candidate version, the topology plan (if any), and each binary's
current-and-candidate path before touching anything; (3) stage each new
binary under a `.new` suffix in the same directory as the live binary, then
atomically rename over the live path (`rename(2)` is atomic on the same
filesystem — this replaces whatever non-atomic copy the current script uses)
rather than overwriting in place; (4) run each replaced binary's existing
health/ready check (the same checks `internal/domain/health` already exposes
per the health registry pattern, not a new one) against the *candidate*, not
a hardcoded default port/binary; (5) hold the process open for a bounded
observation window (e.g. 120s) watching for crash-loop before declaring
success; (6) on any failure at any stage, roll back using the journal:
restore prior binaries via the same atomic rename, restore prior unit files
and config if the topology-migration step touched them, and delete the
journal only on confirmed rollback completion; (7) on daemon boot, check for
a journal file and resume rollback if one exists uncommitted (crash-during
-update recovery). A single-writer lock (`flock` on a lock file outside the
replaced paths, e.g. next to the journal) prevents two concurrent updates.

**Call site.** `scripts/archie-update-install` (journal write, stage-then
-rename, lock acquisition), `internal/releaseupdate` package (health-check
invocation reusing whatever the health registry already exposes — cite the
concrete symbol once located, do not add a second health-check mechanism),
and `internal/app/archied` startup (resume-on-boot journal check, next to
existing bootstrap sequencing).

**Failure mapping.**

| condition | contract error |
|---|---|
| lock held by another update | refuse immediately, name the holder's PID/journal |
| health check fails post-switch | automatic rollback within the observation window, journal records the failure reason |
| crash mid-transaction, journal present at boot | daemon completes rollback before normal startup, logs it |

**Testing.** Stage-then-rename and rollback logic against a fixture
directory tree is fully fakeable — `task check`. Real systemd unit
start/stop as part of rollback needs a real host; manual smoke command.

### Channel, pinning, automation (`#804` item 4)

**Decision.** Add `channel = "stable" | "next" | "<exact-version-pin>"`
to whatever config section already governs update behavior (next to
`ARCHIE_UPDATE_CHANNEL`'s existing chat-channel field in
`internal/releaseupdate/command.go:62` — name this new field distinctly,
e.g. `ReleaseChannel`, to avoid colliding with the existing chat-notification
channel of the same word). `Catalog.Check` filters candidate releases by
this field before returning the newest match. Runtime image pinning: every
shipped profile already pins by digest
(`deployments/*.toml` confirmed), so the remaining gap is
`scripts/archie-update-install`'s fallback default — it must read the
digest from the active profile's config rather than falling back to
`:latest` when none is configured; refuse the update (don't silently pull
`:latest`) if no digest is resolvable and the profile requires a pinned
image. Opt-in auto-update refuses to start when the daemon reports work in
flight (reuse whatever "tasks currently running" signal the daemon already
exposes for graceful shutdown — do not add a second in-flight tracker).

**Call site.** `internal/config/config.go` (new `ReleaseChannel` field),
`internal/releaseupdate` (`Catalog.Check` filtering),
`scripts/archie-update-install` (digest resolution, remove `:latest`
fallback), daemon shutdown/drain signal (reuse for the auto-update
in-flight check).

**Testing.** Channel filtering is pure — `task check`. Digest-refusal path
is a shell-script unit test against a fixture profile.

## Call site inventory

| concern | file | change |
|---|---|---|
| Update contract (Catalog/Installer interfaces) | `internal/releaseupdate/service.go` | none — already handles this |
| Command adapters | `internal/releaseupdate/command.go:22,44-48,62` | new `ReleaseChannel` field distinct from existing chat-channel field |
| Install script body | `scripts/archie-update-install:139-160,170,195-199` | replace clone+compile default with download+verify; add journal, lock, stage-then-rename, digest check |
| CI release publishing | `.github/workflows/deploy.yml:265-316` | none — already handles this |
| Deployment profiles | `deployments/*.toml` | none — already digest-pinned |
| First-install unit creation | `install.sh:414-445` | change — consume shared topology declaration |
| Topology assertions | `deployment_contract_test.go:70-105` | new assertion: profile topology matches install.sh output |
| Topology observation | `internal/app/archied/bootstrap.go` (new sibling code) | new |
| Resume-on-boot rollback | `internal/app/archied` startup | new |
| Health check reuse for candidate verification | `internal/domain/health` | reuse — confirm exact symbol before implementing |

## Execution: multi-agent team breakdown

| sub-feature | issue | implementer scope | suggested council lenses | why |
|---|---|---|---|---|
| Artifact install | item 1 (file if unfiled) | `archie-update-install` download+verify, opt-in source-build flag | `lens-deletionist` | must not leave the clone+compile path as silent default cruft |
| Topology migration | item 2 (file if unfiled) | daemon-side observer, `release.json` schema, `install.sh` consumption, contract test | `lens-boundary`, `lens-operator` | boundary: observation belongs in daemon not shell; operator: refusal-with-plan is the whole safety property |
| Journaled transaction | item 3 (file if unfiled) | journal format, stage-then-rename, lock, resume-on-boot | `lens-operator`, `lens-maintainer` | operator: crash-mid-update is the core failure mode; maintainer: someone must be able to read a journal file during an incident |
| Channel/pinning/automation | item 4 (file if unfiled) | config field, catalog filtering, digest refusal, in-flight guard | `lens-contract` | digest-refusal changes what a config value is allowed to resolve to |

## File and link the beads

- The epic's four numbered work items are not yet individual child beads —
  file one bead per item above, each linking to its matching `##` heading
  here instead of restating the design, with acceptance criteria drawn from
  this doc's failure-mapping tables.
- Add the suggested lenses to each new issue.
