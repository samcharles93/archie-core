# Agent2Agent as archie's agent-to-agent protocol — decision

**Status:** Draft
**Date:** 2026-09-22
**Authority:** the A2A Protocol Specification v1.0.0 (`a2a-protocol.org`) for the
protocol, `docs/architecture/organisation.md` for package placement, and
`docs/architecture/migration-decisions.md` §2 for session and messaging design.

## Decision

A2A is the protocol archie speaks to other agents. It carries agent-to-agent
work: discovery of what a remote agent offers, delegation of a unit of work to it,
progress on that work, and the artifacts it produces.

## Layers

**MCP stays the tool layer.** MCP connects a model to tools and data; A2A connects
agents to agents. They are complementary and archie runs both:
`internal/tools/provider` registers MCP providers as optional capabilities, so an
A2A call and an MCP tool call are different kinds of traffic with different
owners.

**A2A rides archie's existing transports.** A2A's canonical data model is
Protocol Buffer messages and it defines a gRPC binding, so archie speaks A2A over
the gRPC and NATS it already runs. A second wire format, a second broker, or a
bespoke protocol for agent-to-agent traffic is a requirement not to introduce.

## Where A2A stops

A2A is not a general replacement for either of the two protocols archie already
has, and each boundary is a requirement rather than a preference.

- **`internal/domain/messaging` stays the human and channel layer.** A
  Conversation is a ChannelID and a ThreadID, its participants are people on
  Telegram, email, webhook and the dashboard, and its records are what a curator
  reads. An A2A peer is not a channel participant and does not address a
  Conversation.
- **`internal/agentexec` stays the daemon-to-worker execution protocol.** It
  carries a Request, a Result, gate and budget policy, and capture tools between
  the daemon and the agent container it owns. An A2A peer never receives that
  protocol, and A2A never carries gate, budget or protection policy.

A2A is therefore an edge protocol: it exchanges work with peers outside archie's
own process boundary, and it composes with the two protocols above rather than
replacing or abstracting them.

## A2A Task mapping

**Decision: archie's work lifecycle is mapped to A2A's, not expressed in it. An
A2A Task is a view over archie's own task, and archie's status remains the record
of truth.**

This is the load-bearing decision, because A2A's Task carries its own state
machine and archie's lifecycle is the on-disk format of its task store, whose
status strings a rename migrates rather than changes.

The mapping is partial, and each gap is a case where archie knows something A2A's
states do not carry:

- **Completion by another system.** A2A's terminal states describe what the agent
  did (`completed`, `failed`, `canceled`, `rejected`). archie's `merged` and
  `rejected` describe what a forge did to a pull request after archie finished
  its work. There is no A2A state for "an external system accepted this", so a
  completed A2A task cannot express the outcome that matters most to the caller.
- **Handing work to a reviewer.** archie's `pr_open` means the work left
  archie's hands for human review. A2A's `input-required` is the nearest state,
  but it means the _client_ must send more input, and a reviewer is not the
  client.
- **Operator decisions.** `declined`, `parked` and `dead` are outcomes of
  archie's own policy and operator actions. A2A has no state for "the operator
  refused this" or "a budget ran out and an operator must decide".

A WorkflowExecution is the unit an A2A task maps onto, because it is the attempt
that runs, streams and produces artifacts, and a follow-up attempt is a new
execution. A2A's own model agrees that a task is immutable once terminal and that
a refinement is a new task sharing the `contextId`, which is why archie's
per-attempt executions and A2A's per-request tasks correspond.

**Requirement:** the A2A state a peer sees is derived from archie's status by one
mapping, and archie's status is carried on the task so a peer can read what the
derivation lost. The mapping is total in the direction that matters -- every
archie status has an A2A state -- and deliberately not injective, because the
gaps above are facts A2A cannot hold.

**Requirement:** adopting A2A's state names as archie's persisted task status is
rejected. It would migrate the task store, and it would drop the forge outcomes
that tell a caller whether the work was accepted.

## What an A2A agent is

**An A2A agent is an archie Identity.** The Agent Card describes a durable,
addressable actor with an endpoint, capabilities and authentication requirements,
and an Identity is the actor archie persists, versions, suspends and scopes
credentials to.

An `agent.Persona` is not an A2A agent: a persona is prompt text selected for a
turn, and it has no endpoint or credential. An agent container is not an A2A
agent: it is task-scoped and ephemeral, and it terminates with the work it ran.
The daemon is not an A2A agent: it is the process that serves the identity.

## The Agent Card

**The Gateway serves the card, for the identity it is configured as.** A2A's
"Get Agent Card" is an abstract operation with a gRPC binding, so the card is
served through the binding archie runs and needs no HTTP listener.

**The card is derived, never hand-written.** Its skills come from the skill
catalogue, its identity from the configured Identity, and its capabilities from
what the process already advertises about itself. A card that restated
capabilities independently would be a second inventory of the same facts, which
is the class of drift the existing capability reporting exists to prevent.

**The card advertises the authentication archie applies**, and it carries no
secret. A card that named a token would be a credential published at a
well-known location.

## Server before client

**archie is an A2A server first.** Accepting A2A calls is the smaller direction
and the more valuable one: the work a peer would delegate -- an issue taken to a
pull request, through archie's gates, worktrees and review -- exists only inside
archie, and archie already serves its own contracts over gRPC to the Messaging
Service, the dashboard and the State Store. Acting as an A2A client to remote
agents is a later capability, and one that consumes the same contract.

## Authentication and authorisation

**An inbound A2A call is authenticated at the transport, by the same bearer-token
interceptor the Gateway applies to its other contracts.** A2A carries no identity
in its payload by design: identity is established where the connection is, so the
transport token is the identity and no field in a Message or Task names a caller.

**Authorisation is per skill**, which is A2A's own model: the card advertises
skills, and a caller may invoke the skills its credential is scoped to.

**Task-scoped grants are not A2A credentials.** They scope a worker's access to
one task's records inside the State Store and are issued by the daemon for its own
containers. Reusing one as an inbound A2A credential would let a peer act with a
worker's authority, which is the isolation the grants exist to enforce.

**The dashboard's authentication is not an A2A scheme.** It gates an operator
interface, not an endpoint that agents call, so it authenticates no A2A request.

## Package placement

- The contract -- A2A's data model, operations and the mapping from archie's
  status -- lives in `internal/domain/<area>/`.
- The binding that carries it over gRPC lives in `internal/infrastructure/`,
  beside the existing gRPC clients and servers.
- Wiring of the contract to the Gateway and the task store lives in
  `internal/app/`.

## Verification

- A peer fetches the Agent Card over gRPC and receives the configured identity's
  identity, skills and authentication requirements.
- A delegated task reaches an archie task, and the A2A state a peer observes
  changes as archie's status changes, through the one mapping.
- A task that reaches `merged` reports an A2A state derived from it, and the
  archie status is readable on the task.
- A call with no credential, and a call with a credential scoped to another
  skill, are both refused, with the refusal A2A defines for them.
- A task-scoped grant presented as an inbound A2A credential is refused.
- No A2A traffic reaches `internal/domain/messaging` or `internal/agentexec`.
