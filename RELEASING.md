# Releasing

Two components, independently versioned: `archied` (gateway/daemon) and
`archie-agent` (runtime). Tags are `archied/vX.Y.Z` and `archie/vX.Y.Z`.
`CHANGELOG.md` / `CHANGELOG.archied.md` / `CHANGELOG.archie.md` document the
split — see those for what each component actually is.

## The standing rule

**Only version a component that actually has unreleased commits touching its
own package closure.** `tools/release.sh` decides this mechanically (`go list
-deps`, not commit-message guessing — see its header comment), and
`task release:preview VERSION=X.Y.Z` shows you the split before anything is
written. If a component's list comes back empty, or its only entries are
unrelated backlog from before the change you're actually releasing, **skip
it**: pass `RUNTIME=skip` or `GATEWAY=skip`. Do not bump a component's version
just because the other one moved, and do not bundle a large unrelated backlog
into a release "because it's due" — that's a separate release, on its own
review pass, not a rider on this one.

In practice: most sessions touch `archied` (webui, gateway, daemon, chat) and
leave `archie-agent` (the per-task sandboxed runtime) untouched, so most
releases are gateway-only with `RUNTIME=skip`. Don't ask whether to skip an
untouched component — skip it, and say so in the handoff. Only ask when the
runtime genuinely has unreleased changes and there's a real judgment call
about whether to bundle a large pre-existing backlog in or cut it separately.

Note that a runtime code change (e.g. something in `internal/app/agentworker`)
being *merged to `main`* is not the same as it being *in production* — the
archie-agent Docker image isn't rebuilt by a gateway-only release. If a change
needs the new agent image to actually take effect, say so explicitly and
either cut an agent release or note that `docker compose build agent` /
`docker compose pull agent` is needed separately.

## Process

```bash
task release:preview VERSION=1.3.0     # preview what would land, both components
task release:prepare VERSION=1.3.0     # write changelog sections, uncommitted
# edit CHANGELOG.archied.md and CHANGELOG.archie.md -- generated notes
# are a starting point, not the release
task release VERSION=1.3.0             # commit + tag
git push origin main --follow-tags     # CI stamps images with real versions
```

Pass `GATEWAY=<ver>` / `RUNTIME=<ver>` to version the two components
independently, or `skip` on either to hold it back per the rule above. All
three `task release:*` targets forward to `tools/release.sh` — read its header
comment for exactly how it partitions commits and enforces `main`-only,
clean-tree releases.

Pushing is a separate, explicit step — never push without confirming first
(see the repo's general "check before doing anything hard to reverse" rule).
The deploy workflow reads tags pointing at `HEAD`; pushing the commit without
its tags gets images stamped `dev`.

## Gate

CI (`deploy.yml`) runs `task check` before building images, then verifies any
release tag at `HEAD` has a matching `[version]` section in its changelog. A
tag with no changelog entry is a hard failure. No tags at `HEAD` is a warning,
not a failure — images get stamped `dev`.
