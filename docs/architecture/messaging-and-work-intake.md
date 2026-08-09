# Messaging and Work Intake

**Status:** Current state researched; foundation decisions in progress  
**Date:** 2026-07-28  
**Tracking issue:** [#73](https://github.com/samcharles93/archie-core/issues/73)

## Purpose

Archie is a personal assistant and general agentic system. Its interactions
include conversation, questions, notes, reminders, goals, message handling,
troubleshooting, infrastructure operations, server management, source changes,
and open-ended future capabilities.

A message to an agent is therefore not inherently a work request, and invoking
an agent is not inherently a WorkflowExecution.

Messaging and work intake are separate from the Agent System's durable workflow
execution model:

- messaging preserves interactions with people and external systems;
- an agent turn interprets and responds to an interaction using its available
  capabilities;
- work intake handles the optional promotion of an interaction into durable,
  workflow-backed work;
- the Agent System owns Workflow definitions and creates WorkflowExecutions.

Forge issues, Jira issues, Telegram messages, emails, webhooks, and future
channels remain peer external message or artifact sources. None becomes an
execution model merely because it can lead to work.

## Confirmed boundary

Work intake MUST NOT create a WorkflowExecution directly.

Inbound interactions normally route to the Agent selected by the channel
binding. The selected Agent retains its own memory, user knowledge, conversation
continuity, specialisation, and capability context. This continuity is
independent of the underlying model or provider.

Each selected Agent acts through its own immutable IdentityID. Interaction and
resulting action records preserve both that acting identity and the initiating
user identity when one exists.

Agent memory is private by default. Interaction context MAY additionally use
user-wide memory and intentionally global shared memory, but another Agent's
agent-wide memory is not part of the selected Agent's context.

Multiple users may interact with the same Agent. The channel binding and inbound
message MUST resolve both AgentID and initiating User IdentityID before
conversation or memory context is loaded. Sharing an Agent MUST NOT implicitly
share one user's private memory with another user.

An interaction may assemble memory from four scopes: global shared, the selected
Agent's agent-wide memory, the initiating user's authorized user-wide memory,
and the private Agent-user relationship memory addressed by both IDs.

## Conversation identity

Every conversation is identified by the complete tuple:

```text
AgentID
+ UserIdentityID
+ ChannelBindingID
+ ExternalConversationID
+ ThreadID
```

`ThreadID` has an explicit empty value for channels without threads. No element
of this key may be replaced by a display name, configured bot username, channel
type, or other mutable label.

Agent and user memory continue across conversations according to their memory
scope. Immediate conversation history and delivery correlation remain isolated
to the identified channel conversation or thread. Moving between Telegram,
email, or another channel does not create a new Agent or user, but it does
create a distinct conversation unless an explicit linking operation relates
them.

Conversation and message events support projections for channel-entry metrics,
use cases, volumes, outcomes, latency, capability usage, and other product or
operational analysis. Metrics are derived read models; they MUST NOT become
mutable counters embedded in the conversation aggregate or change message
routing semantics.

## Canonical messages

Every inbound, assistant, tool-call, and tool-result message has an immutable
application-generated MessageID. Platform identifiers are external correlation,
not substitutes for the canonical ID.

Messages are addressable because an interaction is more than text. A message may
contain assistant content, reasoning data subject to its retention policy, one
or more tool calls, a tool result with authoritative execution metadata,
attachments, reply relationships, edits, or other typed content. Stable
MessageIDs also allow sessions to branch from an exact prior point.

The canonical session and message implementation must supply:

- application-generated stable message IDs;
- user, assistant, and tool roles;
- assistant tool calls and correlated tool-result messages;
- authoritative tool-result status, timing, error, truncation, and size
  metadata;
- session lifecycle and active-request state;
- session-to-agent-instance and parent-session lineage;
- durable SQLite round trips and timestamp validation.

These semantics must use the confirmed Agent, user, channel-binding, and
conversation ownership model.

The canonical message store MUST preserve messages as immutable records and
represent later edits, deletions, and branches through related records or
events.

Archie requires exact branch-point correlation using stable MessageIDs.

## Conversation branches

A branch is a continuation of a previous conversation at an exact message. A
child conversation records:

```text
ParentConversationID
ForkMessageID
```

Messages remain an append-only linear sequence within each conversation. The
child's effective initial context is the parent's history through
`ForkMessageID`; messages after that point in the parent are not inherited.

After the fork, parent and child are isolated:

- child messages, tool calls, tool results, capability actions, and errors do
  not appear in the parent conversation;
- later parent messages do not silently enter the child;
- returning to the parent resumes its unchanged history;
- the child may itself be branched using the same rules;
- branch lineage remains queryable for navigation, audit, and metrics.

This supports building substantial context about a subject and then performing
one or more contextual subtasks without polluting the original conversation. For
example, a user may branch from a project discussion, create Jira issues in the
child using inherited context, and then return to the project discussion without
adding the Jira tool transcript to the parent.

Branching does not copy mutable message rows. History is resolved through the
immutable parent conversation and fork MessageID, followed by the child's own
message sequence.

Branch isolation applies to conversation history, not to Agent learning or
memory. A memory action performed in a branch has exactly the same semantics as
the same action performed in any other conversation. It writes to its selected
global, agent-wide, user-wide, or Agent-user relationship scope.

Memory provenance MAY retain the source ConversationID and MessageIDs for
explanation and audit. Retrieval and effect are determined by memory scope and
access rules, not by the conversation from which the memory originated.
Returning to a parent does not insert the child's tool transcript into the
parent history, but scoped memories learned in the child remain available to
future retrieval wherever that scope is visible.

Changing sessions or branching therefore controls immediate conversational
context without preventing the Agent from learning durable knowledge.

## Target ownership

The Messaging domain lives at `internal/domain/messaging`. It owns:

- canonical Messages and typed message content;
- Conversations, branches, and lineage;
- channel bindings and external source correlation;
- replies, acknowledgements, and outbound delivery attempts;
- message and conversation lifecycle events;
- domain-defined repository and delivery service contracts;
- projections required for conversation navigation and metrics.

Work intake is a separate cohesive domain at `internal/domain/workintake`. It
owns admission, validation, deterministic routing, and accepted work requests
for the optional transition into durable workflow-backed execution.

Telegram, email, webhook, forge, Jira, and future messaging systems are
infrastructure adapters implementing Messaging contracts. They are users of the
domain; they do not own Messages, Conversations, work admission, or
WorkflowExecution semantics.

The Agent decides whether an ordinary interaction is answered directly, uses a
capability, changes or retrieves Agent-owned information, or proposes durable
workflow-backed work.

Explicitly configured automation MAY enter work intake without a conversational
Agent turn when the external trigger and admission rule already unambiguously
request durable work. This avoids spending an LLM turn to rediscover a
deterministic routing decision.

When an interaction requires durable workflow-backed execution:

1. a channel adapter produces a channel-neutral inbound message;
2. the acting agent handles or interprets the interaction;
3. work intake validates, admits, and routes a proposed work request;
4. work intake emits an accepted work request with its source correlation,
   acting identity, requested outcome, inputs, and routing evidence;
5. the Agent System consumes that request, selects a specific Workflow version,
   and creates the WorkflowExecution.

The accepted work request is a cross-domain handoff, not an alternate execution
aggregate. The Agent System is the sole authority that creates a valid
WorkflowExecution.

An interaction MAY instead be completed directly by an agent turn, delegated to
a tool or capability, recorded as a note or reminder, answered conversationally,
or retained for later context. Those paths MUST NOT require a Workflow or
WorkflowExecution.

A Workflow MAY add reusable, versioned structure to any of those behaviours.
Scheduled personal-assistant routines, message triage, reminders, infrastructure
operations, and other capability sequences are valid Workflow definitions. The
distinction is whether durable structured execution is useful, not whether the
subject originated from a forge.

## Canonical execution language

- **Workflow**: reusable, versioned behaviour definition.
- **WorkflowExecution**: durable execution of a specific Workflow version.
- **WorkflowStep**: a defined operation in a Workflow.
- **StepExecution**: the recorded execution and outcome of a WorkflowStep.
- **Attempt**: a retry within the same WorkflowExecution.

External issues, messages, requests, goals, and notes are not synonyms for these
concepts.

## Current implementation

Current intake is fragmented:

- Telegram, email, and webhook adapters translate payloads into
  `gateway.Message`, while an unused richer `MessageEvent` contract exists
  alongside it.
- `gateway.Router` combines local channel commands, conversational LLM routing,
  and direct task lifecycle operations.
- Telegram is the only channel with production LLM and durable conversation
  wiring. Email and webhook free text currently have no LLM responder.
- Forge polling bypasses the gateway and directly enqueues store rows or NATS
  task messages.
- Chat `/spawn` directly creates a `store.Task` and invents a synthetic forge
  issue number to satisfy the existing persistence key.
- Forge replies and chat approval commands implement separate waiting-human
  paths.
- Message delivery, acknowledgement, retry, deduplication, and source
  correlation are platform-specific side effects rather than one durable model.
- `store.Task` collapses an external source reference and durable execution
  state.

## Current hazards

- Treating every agent interaction as a work request would remove Archie's
  personal-assistant role and force conversation and immediate capability use
  through an inappropriate workflow lifecycle.
- Treating every accepted work request as a WorkflowExecution would let intake
  or channel code select versions and construct invalid execution state outside
  the Agent System.
- Source identity is incomplete: sessions use configured bot usernames, tasks
  use identity strings, and external message IDs are generally absent.
- SQLite and NATS deduplication are based on forge coordinates rather than a
  channel-neutral source-message reference.
- Replies and acknowledgements have no durable delivery-attempt lifecycle, so
  partial success and retry behaviour differ by adapter.
- Production code has two competing channel-neutral message contracts.
- The existing Archie session implementation lacks the required tool-call
  semantics and exact branch lineage and MUST be brought to the canonical
  contract.

## Decisions still required

- the cohesive owner and final location of messaging behaviour;
- the cohesive owner and final location of work-intake admission and routing;
- the canonical inbound message, conversation, and source-correlation model;
- the boundary between an agent turn, direct capability use, and a proposed
  durable work request;
- the exact relationship between an Agent, its acting IdentityID, its user, and
  its channel bindings;
- outbound response, acknowledgement, delivery-attempt, retry, deduplication,
  and failure semantics;
- the smallest accepted-work-request contract consumed by the Agent System;
- migration of forge polling, replies, chat commands, email, webhook, Telegram,
  and Jira integrations.

## Non-goal: semantic search over chat messages

Semantic/vector search over chat messages is an **explicit non-goal** until usage
data shows a real need.

Message search is lexical only: session-scoped SQLite FTS5 queries paged via
`MessageQuery{Query,Limit,Offset}` → `MessagePage`. Do not add embeddings
configuration or vector columns to message records.

The rationale is cost without a demonstrated use case. Measured 2026-08-03 with
`embeddinggemma:300m` — note this was never an infrastructure problem, the model
was already pulled and `ai-sdk/provider/ollama` already has `embed.go`:

- 121 ms median warm per message on the write path, 12.5 s cold;
- 768 dims = 3072 bytes per vector, roughly 15× a typical message payload;
- chronological reads would carry vector data they do not use on the hot path
  of every turn;
- similarity search requires an explicit indexing and retention design.

If this is revisited, vectors belong in a sibling table so chronological reads
never load them.
