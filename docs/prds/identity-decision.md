# Identity for users, agents and external agents — decision

**Status:** Draft
**Date:** 2026-09-22
**Authority:** `docs/architecture/organisation.md` for placement;
`docs/architecture/migration-decisions.md` §3 for the attribution semantics this
document implements; `docs/prds/agent2agent-protocol.md` for the A2A surface that
consumes it.

## Decision

One identity record covers every actor archie has: users, agents, external agents,
service accounts and the system. Authentication is delegated to the estate's OIDC
provider, and attribution stays archie's.

Authentication and authorisation are OSS archie integrates; a design that
hand-rolls either fails this document.

## The provider

**Authelia is the only identity provider**, because the estate already runs it in
front of the dashboard and it issues credentials for both kinds of caller: humans
use the authorisation code flow, and machines use the `client_credentials` grant,
which yields a token whose subject is the client's own identifier.

**Each agent is its own provider client.** A shared client would make two agents
indistinguishable in the audit trail and revocable only together, which is the
property attribution needs and the reason per-client registration is a
requirement.

**A person's client and an agent's client are registered separately**, because the
two token shapes differ and a shared registration would collapse the distinction
the attribution rests on. A person's token names them in `sub`. A machine's token
carries no `sub` at all and names its client instead, so a machine identity is keyed
on its client registration and exists one per client. A token asserting neither is
refused.

**A person's token requests the audience at the authorisation endpoint**, not only
at the token endpoint. A provider that otherwise returns the audience empty
produces a valid token archie must refuse, and that refusal looks like a signing
fault rather than a missing request parameter.

**SPIFFE/SPIRE is not adopted.** It is adopted and maintained, and its attestation
is strictly stronger than a secret: a workload proves what it is rather than what
it holds. It is rejected here on operational fit, not on quality: it adds a
control plane archie does not have, and the property it buys — identity attested
from the platform rather than from a presented secret — answers a threat archie
does not face, because archie's agents run as containers the operator launches and
already receive their credentials from the daemon that owns them. It is the right
answer when agents run on infrastructure the operator does not control.

## What archie owns

- **The identity record.** `internal/domain/identity` survives as archie's own
  record of an actor and is not replaced by the provider's directory, because the
  record carries what the directory does not: the lifecycle, the bindings and the
  attribution below.
- **The lifecycle.** Active, suspended and retired, with rename, suspend,
  reactivate and retire, remain archie's. Suspending an identity must stop it
  acting whether or not the provider still authenticates it.
- **The subject binding.** An identity that proves itself is bound to the provider
  subject that asserts it. This is the same bridge `identity_aliases` already
  provides from a legacy name to an identity, so the second binding is the same
  mechanism rather than a new one.
- **Attribution.** Who acted, and under whose authority.
- **The audit trail of identity changes**, which already records an actor, a source
  and a request identifier for every mutation.

## What archie never owns

A credential, a password, a session, a token, a signing key, a second-factor
enrolment, or a permission rule.

## How an agent proves who it is

**An agent authenticates by presenting a token the provider issued to its own
client**, and archie validates it against the provider's published signing keys and
takes its subject as the acting identity. The agent holds a client secret; archie
holds only the public keys, so a compromised archie cannot mint an identity.

**A WebMCP tool call is not a proof of agent identity and cannot be made one.** A
page can invoke its own tools, so an agent's call and a page script's call arrive
at archie identically. No field, header or convention distinguishes them, because
the difference does not exist on the wire.

**A WebMCP tool call is therefore attributed to the identity the request
presented, with its source recorded as a hint.** It may record that the request
came from the dashboard; it never records that an agent acted, and it never
records that a human approved. A tool call that a page script can forge must not
be the thing an audit trail rests on.

**Agent-initiated action therefore arrives on a surface that carries a credential**
— the A2A surface, or an authenticated agent call — because that is the only place
an agent's identity is verifiable. An approval an agent makes is recorded as an
agent's action.

## The two attributions

Every task action records two identities, and `taskactions` gains both:

- **The acting identity**: the identity that performed the action, derived from
  the credential the request presented and never from a string the request
  supplied.
- **The authorising principal**: the identity whose authority the action used.

**The event kind describes the actor.** An action performed by an agent is
recorded as an agent's action even when a human's standing approval permitted it,
so `KindHumanApproved` is recorded only when the acting identity is a human. An
agent acting under a standing approval records itself as the actor and the human
as the authorising principal.

**An action with no authorising principal is recorded as unattributed rather than
attributed to the actor's own authority.** An agent's own decision and an agent
executing a human's delegation are different facts, and a record that collapsed
them would answer the audit question "approved by whom" with the wrong identity.

**The review gate's approver is the authorising principal of the approval on that
task's action record.** A task parked for a person names that person, not the
surface they used.

## External agents

**External agents are first-class, not a special case.** A peer is an identity of
kind `service_account`, bound to its own provider client, with the same lifecycle
as any other identity.

The cost is a provider client per peer, a subject binding, and a client secret
whose rotation is the peer's responsibility — and retiring a peer is two
operations rather than one, because its record and its provider client are retired
separately.

**A peer's identity is attributed like any other.** The acting identity is the
peer; the authorising principal is the identity that delegated to it, so an
external agent acting for a user records both.

## Authorisation is deferred

**No authorisation engine is built, deployed or configured.** The credential's
scopes are the only permission input, and they are verified by the same provider
that issued them, so the interim authorisation is OSS archie does not maintain.

**Two things are not built now**: a policy language, and a decision point that
answers per-skill or per-task questions beyond a scope.

**When a caller must be limited to a subset of what a scope permits** — A2A's
per-skill model, where a peer may invoke some skills and not others — the decision
point is added behind a single archie interface that takes a subject, an action and
a resource. The engine behind it is an in-process library or a policy service, and
the choice waits for that requirement rather than being made now.

## Verification

- An agent obtains a token from the provider with its own client credentials and
  acts; its identity is recorded as the acting identity.
- A call with no credential is refused, and a call with an expired or
  wrongly-signed token is refused.
- A token issued to one agent is not accepted as another agent's identity.
- A suspended identity cannot act, with a valid provider token.
- A WebMCP tool call records the presented identity and its source, and never
  records an agent as the actor or a human as the approver.
- An agent's approval records the agent as the acting identity and the human whose
  authority it used as the authorising principal, and does not record the human as
  the actor.
- An approval with no authorising principal is recorded as unattributed.
- No password, session, token, signing key or permission rule exists in archie's
  storage.
