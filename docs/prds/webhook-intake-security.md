# Webhook intake security -- decision

**Status:** Decided, not yet implemented
**Date:** 2026-08-22
**Beads issue:** `archie-core-t2db.5`, blocks `archie-core-t2db.4`

## Why this is decided ahead of the code it constrains

`archie-core-t2db` (no-code playbooks) and `archie-core-7d5u` (typed event
sources) both land a public intake surface whose eventual output is an agent
with tool access and a repo. Neither capture (`t2db.1`) nor the intake family
(`7d5u.3`) has shipped yet, so this decision exists to be a contract the two
epics build against, not a retrofit once the surface exists. A webhook URL
that can start an agent is remote code execution with extra steps if any of
the five points below is missing on day one.

## 1. Per-source authentication

Reuse the per-route HMAC-SHA256 scheme already in
`internal/channels/webhook/webhook.go` (`validateHMAC`), unchanged: a source
registers with a shared secret; the signature is checked against the raw
body. Do not invent a second scheme for intake.

**Capture and triggering split at the signature, not at a separate flag.** An
event with a missing or invalid signature is still captured (visible in the
inspector, marked unauthenticated) so an operator can diagnose a
misconfigured sender -- but an unauthenticated event can never be matched
against a binding. Authentication is what promotes an event from "something
arrived" to "something that can run a workflow."

## 2. Human approval before a binding arms

A binding has three states: `draft -> pending_approval -> armed`. Only
`armed` bindings are evaluated against incoming events. Creating or editing a
binding's matcher, mapping, or target workflow always sets it to
`pending_approval`, including edits to an already-armed binding -- an edit is
not silently re-armed, or the approval gate is decorative. Approval requires
an authenticated dashboard session; there is no unattended approval path.

Model: `internal/channels/telegram`'s dangerous-command approval flow
(`dangerousAction` / `pendingApproval` in `dangerous.go` / `approval.go`) is
the existing precedent for "a human decision gates code from running" in this
codebase -- follow its shape rather than designing a new approval primitive.

## 3. Rate and volume limits, per source

A token-bucket limiter keyed by source ID, not global -- one noisy or hostile
sender must not exhaust another source's capture budget. Precedent for
per-key volume capping: `internal/pairing/store.go`'s `MaxPendingPerPlatform`.

On limit exceeded, the request is rejected (HTTP 429), not silently dropped.
A silently dropped event is indistinguishable from a sender that never fired
-- the same invisible-failure shape the update-verification work
(`archie-core-522`) exists to prevent. The rejection itself is observable in
the inspector as a distinct outcome from "not received."

## 4. Blast radius bounded by code, not by prompt wording

A playbook-triggered task runs through exactly the same enforcement as every
other task: the `[[repos.gate]]` quality gate, `diff_cap_lines`, the
sandboxed per-task container (`internal/container`), worktree isolation, and
"the model never runs git." No new relaxation is introduced for
playbook-triggered work, and none is needed -- the existing constraints are
the control.

Mapped payload fields are treated as ordinary user-controlled text inside an
agent prompt, exactly like an inbound chat message today, never as trusted
instructions. "The agent should ignore instructions in the payload" is not a
control and must not appear as one anywhere in this feature -- per
`ARCHITECTURE.md`'s environmental-enforcement-over-prompt-rules principle,
the boundary is the gate and the sandbox, not a sentence in the system
prompt.

## 5. Retention and redaction of captured payloads

**Retention:** captured events (bound or not) are purged after 7 days,
configurable per deployment. Unbound events exist to be mapped against; nothing
justifies keeping them indefinitely once that window has passed, and an
unbounded capture store is the same disk-exhaustion risk item 3 addresses at
the request-rate level.

**Redaction:** before a captured payload is persisted, apply the same
key-name heuristic `internal/gateway/stream.go`'s `sensitiveParameterKey`
already uses for tool-call parameters (matches `token`, `secret`, `password`,
`passwd`, `api_key`, `apikey`, `authorization`, `credential`, `cookie`,
`private_key` against normalized JSON keys) and replace matching values with
the same `[redacted]` marker. This is best-effort, not a guarantee -- a
sender that names a secret field something the heuristic misses will still
have it captured. Document that limitation where captured payloads are
displayed, rather than implying the redaction is complete.

**Visibility:** captured payloads, redacted or not, are visible only to an
authenticated dashboard operator -- the same authorization boundary as every
other webui surface, not a separate or lower bar.

## What this does not decide

The intake family's registration interface (`7d5u.3`), the capture storage
mechanism (`7d5u.2`, embedded NATS), and the binding data model's persistence
and versioning (`t2db.4`) are separate decisions. This document constrains
all three: whatever they build must satisfy points 1-5 above.
