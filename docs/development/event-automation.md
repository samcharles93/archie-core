# Event automation

Authority: `docs/prds/event-automation.md` (the model: source, event type,
mapping, binding, workflow, profile) and `docs/architecture/bindings.md`
(threat model).

## Where each check lives

A binding is checked twice, and a new rule usually belongs in both places:

- **On save**, in `checkedBinding` (`internal/webui/api_binding.go`), so the
  operator sees the problem while editing. It calls the domain checks
  `binding.Validate`, `binding.CompileFilter` and `Binding.CheckWorkflow`.
- **At dispatch**, in `dispatchOneBinding` and `resolveBindingTarget`
  (`internal/daemon`), because the workflow or mapping may have changed since
  the save. A refusal is recorded with `recordDispatchFailure` and starts no
  task.

Put the rule itself in `internal/domain/binding` or
`internal/domain/workflow/task` and call it from both, rather than writing it
twice.

## Adding a binding field

1. Domain type and `Validate` in `internal/domain/binding`.
2. Column, queries, store mapping, wire field, `newColumns`
   ([State Store and persistence](state-store-and-persistence.md)).
3. The request type and `checkedBinding` in `api_binding.go`.
4. The draft, payload and editor in `ui/src/bindings/`
   ([Dashboard](dashboard.md)).
5. Its effect at dispatch, with a case in
   `internal/daemon/dispatch_inputs_test.go` or `dispatch_bindings_test.go`.

## Rules that must hold

- Signing belongs to the source. Intake verifies the HMAC against the source's
  secret before the payload is parsed.
- An event must be identified as an event type before anything dispatches it,
  and a binding applies only to its mapping's event type. Dispatch test
  fixtures therefore create an event type before inserting captures.
- The dispatch ledger is claimed before the task is enqueued (at most once).
- Anything an event supplies that selects what the agent acts on, such as a
  repository, must be checked against configuration. An event never widens
  what an agent can reach.
