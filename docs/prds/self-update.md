# Self-update: artifacts, topology migration, and a recoverable transaction

**Status:** Proposed (rev. 1) -- awaiting sign-off.
**Superseded:** the full analysis, including the production evidence that
motivated it, is tracked in the private issue tracker. This file is a
placeholder so the public branch carries no deployment detail.

## Summary of the proposal

`internal/releaseupdate` (catalog / defer / install / watchdog / verify) stays
the contract. Five phases expand what sits behind it:

1. **Truthful discovery and health.** Discover releases from the forge API
   rather than a local checkout; report "could not check" as a distinct state
   so an unreachable source is never rendered as "up to date"; probe health on
   the address the process actually serves; back up the resolved database path;
   take the runtime version from the image execution actually uses.
2. **Artifact install.** Publish per-platform archives, a checksum manifest and
   a `release.json` as release assets, and install from those instead of
   compiling on the operator host. Keep source build as an opt-in fallback.
3. **Topology migration.** Observe the deployment's shape, have each release
   declare the shape it requires, and execute an approved migration plan
   (units, config additions, dependency-ordered start) when a host must cross a
   process-boundary change. Refuse, with a precise plan, when it cannot.
4. **Journaled transaction.** Pre-flight, durable intent journal outside the
   replaced paths, stage-then-switch, health verification against the candidate,
   bounded post-promotion observation, transaction-wide rollback including
   units and configuration, and resume-on-boot. A single writer lock outside the
   process being replaced.
5. **Channel, pinning, automation.** `stable` / `next` / explicit pin; runtime
   image pinned by digest in every shipped profile; opt-in, windowed
   auto-update that refuses to start with work in flight.

## Open decisions

1. Whether an affected host is migrated by hand now, or held until phase 3 can
   perform the migration.
2. Whether artifact signing is in scope, or digest-vs-manifest is sufficient.
3. Whether unattended auto-update is ever permitted, or approval is permanent.
4. Whether a migration may write configuration on approval, or must always emit
   a diff for the operator to apply.
