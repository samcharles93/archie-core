# Orgs, workspaces and access control

**Status:** Approved
**Date:** 2026-09-24
**Parent:** `docs/prds/event-automation.md`, epic `archie-core-t2db`

## Why this exists

Archie has no tenant boundary and no access control: one operator, one shared
token, and no org on any record. The event automation design needs orgs to own
sources, bindings, workflows and agents, and an agent's reach must be decided
by the policies over it, never by the event that started it.

## Concepts

- **Org.** The tenant boundary. An instance admin creates an org and adds its
  first owner. An owner or admin adds further members by the sign-in
  provider's subject or email.
- **Workspace.** A division of an org, named freely (`networking-prd`,
  `service-desk-dev`), with an optional `environment` attribute (any string)
  that policies can test. Sources, event types, mappings, bindings and runs
  belong to a workspace.
- **Identity.** Every principal is an identity (`internal/domain/identity`):
  - a person is an identity of kind `user`, bound to the sign-in provider
    through the existing subject binding;
  - an agent is an identity of kind `bot` or `service_account`, assigned to
    one org and granted access, secrets and permissions the way a user is.

  Signing in and verifying credentials stay separate from identity; identity
  is who acts. Memberships, roles, grants and audit records all reference the
  identity ID.

- **Instance admin.** Operates the installation: orgs, the sign-in provider,
  instance policies. Instance admins see an org's resources only as members
  of it.

## What belongs where

| Level     | Owns                                                                                                             |
| --------- | ---------------------------------------------------------------------------------------------------------------- |
| Instance  | the shipped workflow catalogue, instance policies                                                                |
| Org       | members, identities and their grants, secrets, agent profiles, workspaces, org workflows and their enabled state |
| Workspace | sources, event types, mappings, bindings, runs, captures, run events and logs, chat sessions, memory             |

Every owned record carries its `org_id`, and workspace records also carry their
`workspace_id`. An identity, secret or profile is used in a workspace only
after the org grants it to that workspace.

A secret is granted to an identity. An agent profile holds the container
image, tools and network access, and no secrets; this supersedes the secret
references in `event-automation.md`'s agent profiles.

A workflow names the identity it runs as. A workflow called by another runs
as its own identity, and the caller's identity must be permitted `run` on it.

## Access decisions

### Where it lives

- `internal/domain/access` owns the policy schema, the action list, how
  principals and resources are turned into policy entities, and the
  `Authorizer` contract.
- The Cedar implementation, using `github.com/cedar-policy/cedar-go`, lives in
  `internal/infrastructure/access`; `internal/app` wires it.
- Exactly two places call the `Authorizer`: the dashboard and API request
  path, and dispatch. Every other service verifies a credential and never
  evaluates policy.

### The model

- **Principal:** an identity, with its memberships, roles and grants.
- **Resource:** the record acted on, with its workspace and org as parents,
  its type, owner and state.
- **Action:** one of `read`, `create`, `update`, `delete`, `approve`, `run`,
  `read_logs`, `read_secret`, `manage_members`, `manage_identities`,
  `manage_policies`.
- **Context:** the event's signature result, the address it came from, the
  time, and the run and step when the principal is an agent.

### The policy chain

```
instance (+ network rules) ─▶ org ─▶ workspace ─▶ identity + workflow ─▶ object
```

- **Instance** policies hold across every org. They include network rules:
  which addresses may deliver events to any source on the instance.
- **Object** policies attach to one object (an event source, a secret, an
  identity, a binding, a workflow): "only `soc-responder` may read this
  secret", "this source accepts events only from 10.20.0.0/16".
- A request is allowed only if every level with policies for it permits it. A
  lower level narrows and never widens; a forbid at any level wins; with no
  permit the request is denied.
- The shipped role policies mean the org level always has policies, so an
  object or workspace policy alone can never grant access.
- Archie ships an instance policy, which no org can remove, forbidding any
  principal to act on a resource outside the orgs it belongs to.

### Storing and changing policies

- Policies are stored in the State Store per level, versioned, and every
  change is an audit event naming who made it.
- A policy is checked against the current schema when it is saved, and every
  stored policy is checked again when archie starts. An invalid org, workspace
  or object policy makes its level deny everything and is reported as a health
  issue naming the policy and the error. An invalid instance policy stops
  archie serving until it is fixed, with the same report.
- A policy change that would leave no owner able to `manage_policies` is
  refused.

### Roles

Archie ships these org roles as policies. A member may hold a different role in
each workspace.

| Role      | Can                                                                                 |
| --------- | ----------------------------------------------------------------------------------- |
| Owner     | everything in the org, including members, identities, policies and deleting the org |
| Admin     | everything except deleting the org and changing owners                              |
| Developer | create and edit workspace resources and org workflows; run workflows; read logs     |
| Viewer    | read everything except secret values                                                |

### Approvals

What needs approval, and who may give it, is org and workspace policy. Archie
ships one default: arming a binding needs a member other than the one who made
the change, holding the admin or owner role. No policy can let a change approve
itself.

### Denials

- A lookup is filtered by org before any policy check, so a record in another
  org is never loaded and is reported as not found, with the same error the
  store returns for a record that does not exist.
- A request denied inside the caller's org is reported as forbidden, without
  the reason.
- Every denial is recorded with the principal, action, resource, the level
  that decided, and the deciding policies. Admins can see this record for
  their org; instance admins for instance policies.

### Recovering from a locked-out org

`archied access reset --org <org>` restores the shipped role policies for one
org and removes its other org-level policies; `--instance` does the same for
instance policies. It runs only on the State Store host, never over the
network, and is recorded as an audit event.

## Credentials

### People and services

- The State Store accepts two kinds of credential: the instance service
  credential, held by the daemon and Gateway for dispatch and background work
  across orgs, with every use attributed; and a principal credential issued to
  one identity, from which the State Store derives the org and workspace scope
  itself. No request field sets scope.
- A person signs in through the provider or uses a personal API token issued
  to their identity. The shared token of a single-operator install
  authenticates as the owner of the `default` org.
- If the sign-in provider is unreachable, sign-in fails with a health issue
  distinct from a rejected credential, `archied access reset` still works, and
  dispatch and running agents are unaffected.

### Agents: decided once, carried as a run credential

1. When a binding dispatches, the orchestrator evaluates the chain for the
   workflow's identity: the identity may `run` this workflow in this
   workspace, and the workflow and binding are enabled.
2. It issues one run credential holding the org, workspace, run, workflow,
   identity, and the capabilities and secrets the identity is granted,
   narrowed by the workflow's policy. This single credential replaces the
   separate State Store and worktree grants.
3. The State Store, forge RPC, worktree RPC and secret retrieval each verify
   every call against the credential and refuse anything outside it, recording
   the refusal on the run. None of them evaluates policy. The State Store's
   task-scoped calls stay limited to updating, transitioning and adding events
   to the credential's own run.
4. The credential lives in the State Store, stored as a digest, so it survives
   a restart. It expires at the workflow's timeout plus a margin, is revoked
   when the run ends, and credentials of runs that are no longer running are
   revoked when archie starts. A run whose credential is refused as unknown is
   parked for retry, not failed.
5. Starting an identity is recorded as an audit event: identity, run,
   workflow, the binding or member that started it, and the event behind it.
   The State Store stamps the actor, principal, org and workspace on every
   event from the credential and ignores those fields in the request.

Forge RPC subjects are keyed by the identity's immutable ID, so identities with
the same name in two orgs never share a responder.

## Events and task identity

- The capture receiver is the only place network rules are evaluated. A
  refused event is not stored; it is counted on its source with the address it
  came from (the most recent addresses only). A source that refuses more
  events than it accepts over an hour is reported as a health issue.
- The task idempotency key is `org/identity/owner/repo/number`, produced in one
  place. The poller and the webhook receiver both resolve the org and identity
  before publishing, so the same issue delivered both ways still gives one
  key. The review-comment key follows the same rule. Task uniqueness in the
  store uses the same fields.

## Upgrading existing installs

- An install with no sign-in provider has one org, `default`, with one
  workspace, `default`, and the local operator as owner.
- The upgrade runs in the State Store first, then in the event store, each
  resumable from a recorded version, after a backup of the database. Every
  existing record gets the `default` org and workspace, and every existing
  identity belongs to the `default` org.
- The State Store refuses scoped calls until its upgrade has finished, rather
  than serving records with no org.
- People's identities and memberships live in the State Store, not the event
  store.

## Audit retention

Audit and denial records are kept for a configured number of days. Repeated
identical denials within a minute are stored once with a count. Their size is
shown on the health surface.

## Delivery order

1. Orgs, workspaces, people as identities, memberships, identities assigned to
   orgs; `org_id` and `workspace_id` on every owned record; the resumable
   `default` upgrade; the new idempotency key.
2. `internal/domain/access` with the chain, shipped roles, the cross-org
   forbid and the startup re-check; applied to the dashboard, API and
   dispatch; denial records; `archied access reset`.
3. Instance and principal credentials with server-derived scope; personal API
   tokens.
4. Identity grants; the run credential replacing both existing grants; audit
   on every start.
5. Approval policy with the shipped default.
6. Instance network rules at the capture receiver, and object policies.
7. Org, workspace and object policy editing.

## Out of scope

- Self-service org sign-up, billing and quotas.
- Groups or teams as principals.
- Agents acting with the permissions of the person who triggered an event.

## Verification

- A member of org A reading an org B record by ID gets the store's ordinary
  not-found error, and org B records never appear in org A's lists.
- A workspace policy permitting what its org forbids has no effect.
- A developer can create a binding and cannot approve it; an admin can.
- A policy naming an unknown attribute is refused when saved; a stored policy
  that became invalid after an upgrade denies its level and shows as a health
  issue.
- A policy change leaving no owner with `manage_policies` is refused, and
  `archied access reset --org` restores access for a locked-out org.
- An identity not granted `secret:firewall-api` cannot retrieve it, and the
  refusal is on its run.
- A run keeps working across a State Store restart, and a crashed run's
  credential is revoked at the next start.
- Every run shows the identity it started and what started it.
- An instance network rule refuses an event from outside its range for every
  org, and the refusal is counted on the source.
- An object policy on a secret narrows which identities can read it.
- A policy forbidding `run` where `environment` is `production` applies to
  every such workspace.
- The same issue delivered by webhook and by poll produces one task.
- A single-operator install upgrades with every record in `default` and works
  unchanged; an interrupted upgrade resumes.
