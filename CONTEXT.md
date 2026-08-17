# Archie Core — Remote Secret Input

The capability that lets an authenticated operator provide a secret value to a
secret engine from a remote surface. A chat channel (initially Telegram)
orchestrates the flow and transports only a one-time entry link; the raw value
is entered through a secure web form and never passes through chat, the model,
logs, or persisted state.

## Language

**Remote secret input**:
The end-to-end capability of delivering a secret value to a named engine
reference from a remote, mobile-first surface. It extends the secret-engine
architecture — it never bypasses an engine's own policy or resolution path.
_Avoid_: remote secret entry, secret provisioning, secret injection

**Secret-input session**:
The tracked lifecycle of one remote secret input: initiation by an
authenticated actor, issue of an entry link, value entry, outcome, and
confirmation. Sessions are bound to the initiating actor and channel, expire,
can be cancelled, and are single-use.
_Avoid_: secret transaction, secret flow

**SecretRef**:
The engine + key pair a value is bound to (the existing `internal/secret`
type). The entry link encodes a SecretRef so the form writes directly to the
exact required safe. _Avoid_: target, destination

**Entry link**:
A one-time, expiring URL with an appended token that binds a SecretRef.
Opening it presents the value-entry form. It is the only thing the chat
channel transports. _Avoid_: magic link (implies authentication), secret link

**Value-entry form**:
The secure web surface where the raw value is typed. It is the only surface
where the raw value exists besides the engine boundary; it never echoes the
value and holds no secret-derived content.

**Writable engine**:
A secret engine that advertises a remote write/update capability for accepted
references. Engines without this capability fail clearly without weakening
resolve-only behaviour.
_Avoid_: settable engine, mutable engine

**Redacted outcome**:
Success/failure feedback (including engine, key, and failure category) that
contains no raw value or secret-derived content. Redacted outcomes are the
only result a user receives.
_Avoid_: result, receipt

**Confirmation**:
The initiating user's acknowledgment, in the initiating chat, that they have
completed value entry. It closes the session and surfaces the redacted
outcome.
