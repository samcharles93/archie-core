# Releasing

One release stream. `CHANGELOG.md` carries every release section, one generated page
per version describes it, and one tag names the release. A release is one version
covering whatever changed; the components' entries are labelled sections inside it
(`### archied`, `### archie-agent`), not separate numbers or separate pages.

The host bundle ships six processes — `archied`, `archie-gateway`,
`archie-state-store`, `archie-ui`, `archie-messaging`, `archie-playbooks` — and one
version covers all of them. `archie-agent`, the per-task sandboxed runtime, ships as a
container image and its changes are the `## [version]` release's `### archie-agent`
section.

`tools/release.sh` decides which commits a release carries from the package closure of
**every shipped binary**, not from commit-message guessing: a change to a package only
the messaging service, state store, playbook runner or UI links still lands in the
changelog. Read its header comment for how it partitions commits and enforces
`main`-only, clean-tree releases.

## What justifies a version

`task release:preview VERSION=X.Y.Z` shows exactly what would land, before anything is
written. A commit set that justifies no version produces no version: `release.sh`
exits **3** with "nothing to release" rather than inventing one, so a pipeline can
finish green without tagging.

Most sessions touch the gateway side (daemon, gateway, web UI, chat, messaging) and
leave the agent runtime alone. That is no longer a per-component `skip` decision — one
version covers the release — but a runtime code change being merged to `main` is still
not the same as it being in production: the `archie-agent` image is rebuilt by the
release, and a host that runs the agent must pull the new image
(`docker compose pull agent`) to get it.

## What the notes are for

**Release notes describe the reader's experience of what shipped.** The test is
the reader, not whether a sentence is negative:

- A capability that **never existed** leaves no expectation to correct, so a note
  about it reads as a feature to someone who never had the thing — out.
- **The implementation's journey** — what was rebuilt, what a guard finally
  covers, what a browser API refuses to surface — is not the reader's experience
  either — out.
- A capability that **shipped but is not yet wired** *is* the reader's
  experience, so it belongs, worded as a caveat on the capability and paired with
  the behaviour that compensates for it.

Design constraints and withheld capabilities belong in `docs/architecture/`,
where implementers look, because there they are requirements rather than news.
Do not apply this mechanically: deleting a caveat a reader needs misleads them,
which is the failure this rule exists to prevent.

## Cutting a release

```bash
task release:preview VERSION=1.3.0     # preview what would land
task release:prepare VERSION=1.3.0     # write the section into CHANGELOG.md, uncommitted
# edit CHANGELOG.md -- generated notes are a starting point, not the release
task release VERSION=1.3.0             # commit + annotated tag
git push origin main --follow-tags     # CI stamps images with real versions
```

`release:prepare` emits bare commit subjects; they are a starting point. Rewrite them
to say what a reader gets, per "What the notes are for" above.

Pushing is a separate, explicit step — never push without confirming first (see the
repo's general "check before doing anything hard to reverse" rule). The deploy
workflow reads tags pointing at `HEAD`; pushing the commit without its tag gets images
stamped `dev`.

## What a release publishes

Tagging one version produces, from a single `deploy` run:

- **Container images** — `ghcr.io/samcharles93/archied:latest` and
  `ghcr.io/samcharles93/archie-agent:latest`. The runtime image is rebuilt by the
  release, so runtime commits merged to `main` do not reach a running sandbox until the
  host pulls the new image.
- **`:latest` names the newest release, not the newest commit.** A push to `main`
  that is not a release publishes `archied:edge` (mutable, tracking `main`) and
  `archied:sha-<commit>` (immutable) instead, so a release's `:latest` stamping is
  not overwritten minutes later by the next ordinary commit. A host that runs
  releases follows `:latest`; a host that tracks `main` follows `:edge`.
- **A GitHub Release with a distribution zip** — `dist-zip` builds every process the
  reference deployment runs (`archied`, `archie-gateway`, `archie-state-store`,
  `archie-ui`, `archie-messaging`, `archie-playbooks`) for linux/amd64, packs them with
  `deployments/INSTRUCTIONS.md` and `CHANGELOG.md`, and attaches the zip to the
  Release. The Release body is that version's `## [version]` section of `CHANGELOG.md`.

The zip is also uploaded as an Actions artifact, but that is a backup copy: artifacts
expire after 90 days and need an Actions login to download. The Release asset is the
durable, operator-reachable one.

## Gate

CI (`deploy.yml`) runs `task check` before building images, then verifies any release
tag at `HEAD` has a matching `[version]` section in `CHANGELOG.md`. A tag with no
changelog entry is a hard failure. No tags at `HEAD` is a warning, not a failure —
images get stamped `dev`.

One consequence worth knowing when a release commit needs amending: GitHub Actions
runs the workflow **from the commit that triggered it**, so a fix to `deploy.yml` only
takes effect for a release if it is an ancestor of the tagged commit. Delete and re-cut
the tag (`git tag -d`, `task release`) rather than committing the fix on top of an
already-tagged release.
