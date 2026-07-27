# Changelogs

Archie has two independently versioned components:

- [CHANGELOG.archied.md](CHANGELOG.archied.md) — `archied`, the user-facing gateway and control plane.
- [CHANGELOG.archie.md](CHANGELOG.archie.md) — `archie-agent`, the agent runtime.

Release tags use `archied/vX.Y.Z` and `archie/vX.Y.Z`. The gateway combines
the matching component entries when it reports an installed or available
update.
