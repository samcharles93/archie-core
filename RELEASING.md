# Releasing

Two components, independently versioned: `archied` (gateway/daemon) and
`archie-agent` (runtime). Tags are `archied/vX.Y.Z` and `archie/vX.Y.Z`.
`CHANGELOG.md` / `CHANGELOG.archied.md` / `CHANGELOG.archie.md` document the
split — see those for what each component actually is.

The UI Service (`archie-ui`) is not a third component: it ships inside the
`archied` release, versioned by the same `archied/vX.Y.Z` tag. It shares
`internal/webui` (dashboard HTTP layer, embedded SPA) with the daemon, so the
two move together; `tools/release.sh` extends archied's `go list -deps`
closure with `cmd/archie-ui` and `internal/app/archieui` for exactly this
reason — without them a commit touching only the UI process would land in
neither changelog.

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

In practice: most sessions touch `archied` (webui, gateway, daemon, chat,
archieui) and leave `archie-agent` (the per-task sandboxed runtime)
untouched, so most releases are gateway-only with `RUNTIME=skip`. Don't ask
whether to skip an untouched component — skip it, and say so in the handoff.
Only ask when the runtime genuinely has unreleased changes and there's a
real judgment call about whether to bundle a large pre-existing backlog in
or cut it separately.

Note that a runtime code change (e.g. something in `internal/app/agentworker`)
being *merged to `main`* is not the same as it being *in production* — the
archie-agent Docker image isn't rebuilt by a gateway-only release. If a change
needs the new agent image to actually take effect, say so explicitly and
either cut an agent release or note that `docker compose build agent` /
`docker compose pull agent` is needed separately.

## What the notes are for

**Release notes state what a reader gets.** A reader scanning releases does not
need to know what was withheld, what a browser API cannot do, or what the
engineering took; and a note about an absence reads as a feature to someone who
never had the thing. Design constraints, withheld capabilities and failure modes
belong in `docs/architecture/`, where implementers look, because there they are
requirements rather than news.


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

## What a release publishes

Tagging both components at one commit produces, from a single `deploy` run:

- **Container images** — `ghcr.io/samcharles93/archied:latest` and, when the
  runtime moved, `ghcr.io/samcharles93/archie-agent:latest`. A gateway-only
  release (`RUNTIME=skip`) does not rebuild the agent image, so runtime commits
  merged to `main` do not reach a running sandbox until an agent release is cut
  and the host pulls the new image.
- **A GitHub Release with a distribution zip** — `dist-zip` builds every
  process the reference deployment runs (`archied`, `archie-gateway`,
  `archie-state-store`, `archie-ui`, `archie-messaging`, `archie-playbooks`) for
  linux/amd64, packs
  them with `deployments/INSTRUCTIONS.md` and the changelogs, and attaches the
  zip to the Release for the **archied** tag. The Release body is that
  version's `CHANGELOG.archied.md` section. Both jobs are keyed to the archied
  tag, so a gateway-only release still produces the zip.

The zip is also uploaded as an Actions artifact, but that is a backup copy:
artifacts expire after 90 days and need an Actions login to download. The
Release asset is the durable, operator-reachable one.

## Gate

CI (`deploy.yml`) runs `task check` before building images, then verifies any
release tag at `HEAD` has a matching `[version]` section in its changelog. A
tag with no changelog entry is a hard failure. No tags at `HEAD` is a warning,
not a failure — images get stamped `dev`.

One consequence worth knowing when a release commit needs amending: GitHub
Actions runs the workflow **from the commit that triggered it**, so a fix to
`deploy.yml` only takes effect for a release if it is an ancestor of the tagged
commit. Delete and re-cut the tags (`git tag -d`, `task release`) rather than
committing the fix on top of an already-tagged release.

