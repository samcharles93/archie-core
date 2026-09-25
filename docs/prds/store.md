# Store

**Status:** Approved
**Authority:** `docs/architecture/plugins-and-extensions.md`; supersedes
`docs/prds/extensions-center.md`, whose installed-inventory states are
adopted below.
**Compounds with:** `docs/prds/external-agent-harness.md` (harness Kits),
`docs/prds/pr-review-agent.md` (the first package).

## Decision

Operators get capabilities from a store: browse, install, update and remove
packages from the dashboard, with no config file editing. A package is an
OCI image in a registry. A store catalogue is a list of package references.
Installing pins a digest. Every package declares the authority it needs, and
an update that asks for more stops for the operator's approval.

## Packages

Two kinds, both ordinary OCI images:

- **Harness Kits.** Kit v3 images, resolved as
  `docs/prds/external-agent-harness.md` decides. Their authority is their
  Kit permission surface.
- **Archie packages.** An image whose manifest annotation
  `dev.archie.package.v1` carries the descriptor and whose single layer
  carries files. A package contributes any of: workflow definitions,
  prompts, skills, MCP server definitions, and default settings for them.

Archie packages are declarative. They carry no Go, Yaegi or script code that
archie executes on the host. Anything that runs code ships as a Kit and runs
in a sandbox. An archie package that references a Kit names it by digest,
and installing the package installs the Kit.

The descriptor declares: display name, description, version, the families
it contributes, the packages it requires, and its authority.

## Authority

A package's authority is the set of grants it needs to work:

- credential services (from its Kits and MCP servers);
- egress hosts (from its Kits);
- forge permissions its workflows use: read, comment, review, push, open
  pull request;
- triggers it registers: watched-repository events, schedules;
- tools it enables for agents.

Install shows this list and records the operator's acceptance against the
digest. An update whose authority is equal or narrower applies under the
package's update policy. An update whose authority is wider waits for
approval, showing only what widened. Nothing a package does at runtime can
exceed its accepted authority: archie checks forge actions, triggers and
tool grants against it, and the egress proxy enforces the Kit's grants.

## Catalogues

A catalogue is an OCI artifact listing package references with digests,
signed by the catalogue's key. Archie ships with the default catalogue and
its public key configured. An operator can add catalogues, for example a
private registry, by reference and public key. A catalogue without a key is
refused unless the operator marks it unsigned, which the dashboard shows on
every package it supplies.

Install and update verify the catalogue signature and the package digest.
Archie never installs a tag it has not resolved to a digest through a
verified catalogue.

## Install, update, remove

- Installed packages, their pinned digests, accepted authority and update
  policy are State Store resources. They apply without a restart.
- **Update policy** per package: `manual` (default) or `auto`. `auto` applies
  non-widening updates when the catalogue lists them; widening updates always
  wait.
- **Setup.** A package whose authority includes a credential with no binding
  installs as `needs_setup`, and links to the binding form or the harness
  setup terminal. Its triggers do not fire until setup completes.
- **Remove** stops its triggers, removes its contributions, and leaves any
  credential bindings for the operator to delete. A package another
  installed package requires cannot be removed first.
- Rolling back reinstalls the previously accepted digest.

## Inventory

The installed view lists every package and every bundled capability with
one state, computed on read and never persisted:

| State         | Meaning                                                  |
| ------------- | -------------------------------------------------------- |
| `available`   | in a catalogue or bundled, not installed                 |
| `needs_setup` | installed, waiting for a credential binding              |
| `active`      | installed and running                                    |
| `degraded`    | active, and its family's health check reports a fault    |
| `pending`     | an update waiting for approval of widened authority      |

Core capabilities (identity, workflow engine, State Store) are listed and
cannot be removed.

## Out of scope

- Payment, licensing and paid packages.
- Publishing from the dashboard. Packages are built and pushed with standard
  image tooling.
- Ratings and reviews.

## Plan

1. Archie package descriptor and its validation, including declarative-only
   content.
2. Installed-package resources, digest pinning, install and remove for
   archie packages from a local registry.
3. Authority model: extraction from packages and Kits, acceptance records,
   runtime checks for forge actions, triggers and tools.
4. Signed catalogues and verification; the default catalogue.
5. Updates: widening detection, approval, `auto` policy, rollback.
6. Kit packages through the same flow; `needs_setup`.
7. Inventory states across packages and bundled capabilities.
8. The PR review agent published as the first package.

## Verification

- Installing the PR review package from the default catalogue on a fresh
  instance, then binding a credential, reviews a PR without editing
  configuration.
- An update adding a forge permission or egress host waits as `pending`,
  showing only the added grant, and does not apply under `auto`.
- A workflow from a package that tries a forge action outside its accepted
  authority fails the step.
- A tampered package digest or an unsigned catalogue refuses to install.
- A package containing executable host code is rejected at validation.
- Rolling back restores the prior digest and its accepted authority.
