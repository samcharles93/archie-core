# SOUL: an agent's user-authored personality

**Status:** Draft
**Authority:** `docs/architecture/identity.md`, “Three-layer identity model”;
`docs/prds/orgs-and-access.md` for identity and org ownership.

## Decision

SOUL supplies an agent's name, register and warmth. It belongs to the agent's
immutable `IdentityID`, not its session, model, installation or container
profile. Rename preserves it; changing a profile does not change who acts.
SOUL never changes attribution, credentials, grants or identity lifecycle.
`config.AgentProfile` remains an execution environment, not another identity.

Use the existing three layers: invariants, user-authored SOUL, and curator-derived
representation. Curators may propose SOUL changes but never apply them.

## Files and ownership

The config directory is `${XDG_CONFIG_HOME:-~/.config}/archie`, or the directory
of an explicitly selected config file. The default/root agent uses `SOUL.md`
directly there; additional agents use `agents/<identity-id>/SOUL.md`. The root is
the identity selected by application composition, never the initiating user or
a session's personality selection. No file creates or selects an identity.

The common case keeps a human-friendly filename; additional agents get separate
asset directories, following `.agents/skills/<name>/SKILL.md` without confusing
these directories with `[containers.profiles.<name>]`. Reject display-name or
slug directories: rename and same-name agents across orgs would require another
name-resolution rule. Immutable IDs avoid both problems without an org directory.

When a file-owned agent's file is absent, create the shipped default with
exclusive creation. Never overwrite an existing file, including an empty one.
A creation failure still permits the turn using the embedded shipped default.
Ship that document independently of `agent.ShippedPersonas`, so deleting personas
cannot delete the fallback.

Resolve once at the start of every new generation:

1. A stored SOUL entry for the acting identity is authoritative.
2. Without an entry, read that identity's file again.
3. An absent, empty, unreadable or invalid selected source uses the shipped default.

The first explicit dashboard/API save takes ownership permanently. Thereafter
file edits are ignored: emit one ownership notice and show the owning stored
version in the dashboard. Restoring the shipped default writes a stored value;
it does not remove ownership. Refuse writes that remove an owned entry.

This ownership boundary is the design's one stateful rule. Files seed the
editable value, not an automatic durable import. Reject seed-once import because
it breaks next-message file editing; reject file-always-wins because it makes
dashboard saves ineffective. Neither a restart nor an upgrade overwrites an
owned value, following the precedence of `controlplane.AgentProfileKind` without
copying its startup import timing.

## Prompt and failure contract

Keep `archie.md.tpl`'s XML-escaped
`<soul purpose="identity_and_style" trust="data">` block before
`<instruction_precedence>`. SOUL is style reference data; the invariant sections,
tool inventory and runtime metadata remain authoritative and uneditable.
Preserve `TestBuildSystemPromptSoulCannotOverrideInvariants`.

Accept UTF-8 plain Markdown with at most 8 KiB in its XML-escaped body. This bounds
personality to a small prompt contribution; count the complete rendered prompt
in the turn's context budget and never truncate invariant sections. Bound file
reads before decoding; reject oversize input rather than truncating instructions.
File and stored writes use the same validator; an invalid save leaves the stored
version unchanged. The shipped default must pass that validator.

Read and creation errors never fail a turn. Log once per identity/source failure
episode, retry on the next turn, and allow a new warning after recovery. If the
store cannot establish whether an owned entry exists, use the shipped default,
not a potentially shadowed file. Do not log SOUL text. Each turn keeps its resolved
snapshot; edits affect the next generation without restart or SIGHUP.

## Cross-layer delivery

- `internal/domain/agent` owns SOUL content validation and the identity-keyed read
  contract; `internal/domain/identity` remains the actor/lifecycle authority.
- The configuration infrastructure resolves the config directory and handles
  file discovery, exclusive default creation and bounded per-turn reads.
  Application composition resolves the acting `IdentityID` through the existing
  identity repository/bindings and supplies it to the gateway.
- Register `SoulKind` (`agent-souls`) in the control plane with a live descriptor
  and an org-scoped document mapping identity IDs to `{text}`. Persist it through
  the State Store's existing resource/history contracts, not a new table,
  process or SOUL RPC. An empty map owns no identity; saves preserve other entries
  and use expected-version checks. Validate that each key is an agent identity
  in the resource's org; begin with the default org without inventing org setup.
  Every save records the authenticated editor's identity, source, request ID and
  timestamp; file installation records the system identity, not a fictitious user.
- Replace `TurnRunnerConfig.Personas` / `PersonaPromptSource` with the per-identity
  source in `prepareTurn`; name the `SystemPromptConfig` input SOUL. Both normal
  and streaming turns use this assembly path. Do not add personality to the
  worker environment, `AgentProfile` or workflow-stage prompts.
- On an agent identity's dashboard page, expose its effective SOUL, file/default
  or stored source, shadowed file path, owning version, and Markdown editing.
  Save to the control plane, never the host file; show validation/version
  conflicts and offer restoring shipped content or a history revision as a save.
  Read effective file/default content and ownership metadata through a gateway
  query using the turn resolver; the separate UI process never reads the
  gateway host's files. Apply identity-management access rules and the existing
  shared live-update hub.
- Update `docs/development/agent.md` and `control-plane.md` with this reach and
  precedence, `frontend-ui.md` with the gateway query, and the config wiring
  guidance with a copyable file example.

## Persona removal gate

SOUL supersedes `PersonaRegistry` and the persona-reconciliation proposal;
identity-per-action supersedes personas as an identity mechanism.
`PersonaRegistry.GetActive` composes the baseline with one of five styles from
`agent.ShippedPersonas`; SOUL replaces the baseline, not that style catalogue.
Per-session switching disappears: remove `/personality`, Telegram personality
callbacks, the dashboard persona page, and chat wire fields `personas`,
`active_personas` and `personas_available` without reusing their field numbers.
Do not merge session selections into SOUL or retain a separate style axis.
Custom persona text must remain available to copy before deleting its stored
resource; no migration guesses one agent's SOUL from several sessions.

Remove persona loading, selectors and commands only after every chat composition
supplies the identity-aware SOUL source, the fallback renders without personas,
and the dashboard can edit it. SOUL delivery does not wait for org creation or a
new container-profile model.

## Acceptance

- Two agents use distinct files and stored entries; renaming one or sharing a
  container profile does not change or mix their SOULs.
- Before ownership, a file edit changes the next normal or streaming prompt;
  creating defaults never clobbers an existing file.
- After a save, conflicting file edits and restarts cannot override the stored
  value; the ignored file and owning version are visible. Updating one agent
  preserves the other; stale saves fail and history restores are audited saves.
- Missing, empty, unreadable, malformed and oversize selected inputs render the
  shipped default. Repeated read failures log once, recover on a later turn,
  and never abort generation.
- Hostile markup stays escaped before singular, unchanged invariant blocks;
  prompt budgeting includes SOUL and cannot drop the rules.
- Dashboard edits reach the next generation without restart; persona removal
  leaves every channel with a nonempty identity slot and preserved custom text.
