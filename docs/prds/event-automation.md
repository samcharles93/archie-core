# Event automation: identified events, typed parameters, purpose-built agents

**Status:** Approved
**Date:** 2026-09-23
**Parent:** epic `archie-core-t2db`

## Why this exists

Archie is an event-driven automation system. Any system that can send a
webhook (a forge, an error tracker, a firewall, a monitoring stack) should be
able to start an agent built for that job. The shape this replaces cannot:

- A binding matches only the URL segment an event was posted to, and a source
  may have one binding. Two kinds of event from one sender cannot go to
  different workflows.
- A mapping is a list of JSON paths written against one example payload.
  Nothing recognises what kind of event arrived.
- Mapped values reach the task only as `key=value` lines in its body text.
- Every task needs a repository and runs in a git worktree.
- Every agent runs the same image with the same forwarded keys.

## The model

```
source ─▶ event type ─▶ mapping ─▶ binding ─▶ workflow ─▶ agent profile
          (identified)  (params)   (+ filter,  (inputs,     (image, tools)
                                    repo)       profile)
```

### Sources

A source is the endpoint one sender posts to. Its path is a generated UUIDv7
(`/webhooks/capture/0199…`), so an endpoint cannot be guessed or collide. The
operator may replace it with a custom path for a sender that cannot use that
format; a custom path must be unique and URL-safe, and is refused otherwise.
A source owns the signing setting:

- **Signed** (default). Archie generates the secret. Only events with a valid
  `X-Hub-Signature-256` or `X-Signature-256` HMAC are dispatched.
- **Unsigned.** The operator turns signing off for a sender that cannot sign.
  The source and every event from it are marked unsigned in the inspector,
  the binding list and the task timeline. Turning signing off needs the same
  approval as arming a binding.

Unauthenticated events on a signed source are captured and never
dispatched.

### Event types

An event type is a named kind of event from one source, such as
`github / pull_request.opened` or `firewall / blocked_connection`. It is
defined by a match rule:

- header equality (`X-GitHub-Event = pull_request`), and
- payload conditions (`action = "opened"`, or a path being present).

An event type can be created two ways:

- **From an example payload.** The operator pastes a payload (and optionally
  its headers), and archie converts it to the type's schema. No event needs to
  have arrived.
- **From captures.** Archie proposes types from what has arrived, as below.

Either way, archie infers the schema, not the operator:

- Each capture gets a structural signature: the set of JSON paths with their
  value types, plus the headers that look like discriminators (`*-Event`,
  `*-Type`, `Content-Type`).
- Captures from one source that share a signature are grouped. The inspector
  shows each group as a proposed event type, with its inferred schema, its
  number of events and a sample, until the operator names it or dismisses it.
- The proposed match rule is the set of discriminators that separates the
  group from its siblings. The operator can edit it before saving.
- A capture that matches no named type is shown as unidentified. It is never
  dispatched.

Two event types on one source whose rules can match the same event are
refused when the second one is saved.

### Mappings

A mapping belongs to an event type. It maps payload paths to named, typed
parameters, and it is checked against the type's inferred schema, so a path
the type does not have is refused when the mapping is saved. Any number of
bindings may reuse one mapping.

Each mapping shows how many events it has matched, with the time of the most
recent, so an operator can see which mappings are live and which have never
fired.

### Workflows declare inputs

A workflow definition declares its inputs:

```yaml
id: firewall-investigate
inputs:
  src_ip: { type: string, required: true }
  severity: { type: string }
repository: none # none | optional | required
profile: network-investigator
steps: ...
```

- `repository: none` runs the agent in a scratch workspace with no clone.
  `required`, the default, gives the agent a worktree of the task's
  repository. `optional` gives it a worktree when the binding names a
  repository and a scratch workspace when it does not.
- A workflow whose `repository` is not `required` may use only steps that need
  no repository. `agent.run` is one: it runs one agent mission and finishes
  the task as done, with no pull request.
- The declared inputs reach the agent as structured data in the task brief
  and in the agent's context. They are not body text.
- A workflow may declare `outputs`, the structured result it returns when it
  finishes.

### Workflows call workflows

A workflow step can call another workflow. The call passes the callee's
inputs, and the callee runs as its own run with its own agent profile, so one
event can start an investigator that then calls a containment workflow:

```yaml
steps:
  - type: workflow.call
    workflow: firewall-contain
    inputs: { src_ip: inputs.src_ip }
    wait: true # false starts the callee and continues
```

- With `wait: true` the caller receives the callee's outputs and fails if the
  callee fails. With `wait: false` the caller continues and the callee's
  outcome is reported on its own run.
- A call's inputs are checked against the callee's declared inputs when the
  calling workflow is saved.
- A workflow that calls itself, directly or through other workflows, is
  refused when it is saved. Calls are limited to a fixed depth at run time.
- A callee must be enabled for the caller's org.

### Bindings

A binding connects an event type to one workflow:

- an optional filter on mapped parameters (`severity in ["high", "critical"]`),
  written in the CEL the playbook engine already uses;
- an assignment of workflow inputs from mapped parameters or constants,
  checked against the workflow's declared inputs when the binding is saved;
- a repository, when the workflow's `repository` is not `none`: either fixed
  (`acme/api`) or taken from a parameter (the GitHub webhook's
  `repository.full_name`). A repository taken from a parameter must be a
  configured one, or the event does not dispatch.

Any number of bindings may use one event type. Approval and re-approval after
an edit are unchanged. A binding whose workflow input types no longer match is
refused at dispatch and reported on the binding.

### Agent profiles

A profile is a named execution environment that a workflow selects:

- the container image;
- an allowlist over the tools Archie adds to the agent: MCP servers,
  repository scripts and skill plugins. The agent's own file tools are always
  present;
- the secrets injected into it, by reference to the secret store, never
  inline (superseded by `docs/prds/orgs-and-access.md`: secrets are granted to
  identities, and a profile holds no secrets);
- its network access and forge access (superseded by
  `docs/prds/orgs-and-access.md`: permissions are granted to identities).

Profiles are configured under `[containers.profiles.<name>]`. The global
`[containers].image` becomes the default profile, so existing workflows are
unchanged. A task whose workflow names an unconfigured profile is parked for
an operator. A secret or permission is granted to an identity, never to a binding, so an
event can never widen what an agent can do (`docs/prds/orgs-and-access.md`).

### Workflows belong to orgs

Archie ships a default workflow catalogue that every org can use. An org can
add its own workflows, and can enable or disable any workflow, default or its
own, for itself. A binding can target only a workflow enabled for its org, and
disabling a workflow stops its bindings dispatching and marks them on the
binding list.

## Delivery order

1. Event types: structural signatures, grouping, naming, match rules, and the
   inspector view of proposed and unidentified events.
2. Mappings per event type, and multiple bindings per source.
3. Workflow inputs, structured parameters in the task brief, and
   `repository: none | optional | required`.
4. Agent profiles with image and tools.
5. Signing as a source setting, with the unsigned option; UUIDv7 source paths
   with a custom-path override.
6. Workflows calling workflows, with declared outputs.
7. Org-scoped workflow catalogue, once orgs exist.

Each step ships usable on its own. Step 3 is the first where an event that has
nothing to do with a repository can run a workflow.

## Out of scope

- How forge-issue playbook triggers (`internal/domain/eda/playbook`) relate to
  event types. They stay as they are until event types exist.
- Built-in catalogues of event types for known senders.
- Orgs themselves: membership, access, and how sources and bindings belong to
  one. This design needs only an org to own workflows and their enabled state.
- Running an agent outside a container.

## Verification

- Two GitHub webhooks with different `X-GitHub-Event` headers posted to one
  source are grouped into two proposed event types, and a firewall payload
  with a different shape is grouped separately.
- A capture matching no named type is shown as unidentified and starts
  nothing.
- A firewall event runs a `repository: none` workflow with its mapped inputs
  readable by the agent as structured data, and no clone is made.
- A GitHub `pull_request.opened` event runs a PR review workflow against the
  repository named in the payload.
- An event type created from a pasted payload has that payload's schema, and a
  later matching event is identified as it.
- A new source gets a UUIDv7 path; a custom path that is already taken is
  refused.
- A workflow that calls another with `wait: true` receives its outputs; one
  that calls itself through another workflow is refused when saved.
- A mapping's matched-event count rises by one for each event it resolves.
- A workflow disabled for an org cannot be targeted, and its existing bindings
  stop dispatching.
- A binding whose filter excludes an event does not dispatch it.
- An agent receives only the secrets its identity is granted.
- An unsigned event on a signed source is captured and not dispatched. An
  event on an unsigned source is dispatched and marked unsigned.
