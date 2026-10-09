# AGENTS.md

Rules for any agent working in this repository. Short on purpose: every rule
here changes a decision. If a rule stops changing decisions, delete it.

## What this is

**archie-core** is event-driven agentic automation. An event (an issue, PR,
webhook, schedule, chat message) is matched to a workflow, the workflow runs an
agent or tool in an isolated container, and the result lands somewhere for
review or action. A repository is an optional input to a workflow, not the point
of the product.

<temporary - this statement must be removed on 2026-10-10"> The product is
unfinished. Many features are half-built, and the codebase carries far more
code, binaries, docs and tooling than it delivers. The job right now is to
finish features and shrink the codebase, not to extend it. </temporary>

## Authority

When sources disagree, the higher one wins:

1. The maintainer's instruction in this session.
2. This file.
3. The code as it is in the tree.
4. Beads issues (`bd show <id>`): the plan for a piece of work.
5. Everything else (memories, comments, old docs) is a hint. Verify it against
   the code before acting on it.

## Working with the maintainer

- **A clear direction is the go-ahead.** Do it in the same turn. Do not restate
  the plan, offer options, or ask "shall I proceed?". Stop only for destructive
  or outward-facing actions (deleting data, pushing, touching a live host).
- **Post outside the repo only when told.** Chat, tickets and any external
  service get a message only on the maintainer's instruction.
- **Report, don't narrate.** Final replies say what changed, what was verified,
  and anything blocked, in a few lines. No preamble, no recap of the request, no
  closing offers of more help. Leave out what the maintainer can see or infer:
  a clean tree, unpushed commits, a passing gate (commits imply it), and steps
  skipped that the task never asked for. Name a skipped step only when the
  task required it.

## How to decide

- **Broken beats new.** A bug that blocks work gets fixed now, with the smallest
  change that unblocks it. No design docs and no new mechanism.
- **Cheapest option first.** Before building, name the smallest change that
  solves the problem, including changing configuration or deleting something. If
  your plan adds a new mechanism (RPC, process, binary, abstraction, config
  field, tool) or grows past about 100 lines, stop and assess whether you're
  taking the path of least resistance.
- **Delete before adding.** Unused code, dead paths, fallbacks nobody takes and
  features nobody runs get removed, not maintained. Solo project, single user:
  breaking changes need no compatibility shims.
- **Finish before starting.** Prefer completing a half-built feature end to end
  over starting a new one.
- **Services own contracts.** A service is a gRPC contract in
  `proto/<service>/v1` and one binary that serves it. Services reach each other
  only through those contracts, never through another service's tables or
  packages. A new command is a subcommand of an existing binary. A new binary
  exists only for a new service or a real process boundary.
- **No compensating tools.** Do not write a linter, checker or generator to
  enforce something a simpler design would make unnecessary. Fix the design.
- **A setting must work end to end.** A config field that parses but has no
  consumer is a bug. Settings are edited through the control plane (API and
  dashboard), never by requiring a server login.
- **Ask only for real decisions.** Product direction and trade-offs the
  maintainer owns are questions. Everything with a conventional answer is not:
  decide, do it, say what you chose.

## Code

- Idiomatic Go. Small functions, explicit errors, no package-level mutable
  state, no god objects. Each feature wires itself; a composition root only
  calls those constructors in order.
- Dependencies point inward: `cmd → app → {domain, infrastructure}`,
  `infrastructure` implements `domain` interfaces, `domain` imports neither.
  `cmd/` parses flags and calls `app`, nothing else.
- Fix invalid state where it is produced, not with checks where it is consumed.
- If a fix fails twice, throw the block away and rewrite it simply.
- Comments say why, briefly. No migration history, phase names or issue
  narration in code.
- Generated files (`ui/dist/`, protobuf, sqlc) are regenerated and committed
  with the change that caused them, never hand-edited.

## Tests

The default for a change is no new test. Write one only when:

- something actually broke: write the test that reproduces it, see it fail on
  the assertion, then fix; or
- the change alters one of these seams: State Store SQL semantics (transition
  guards, idempotent dispatch, cancellation), security boundaries (HMAC, grants,
  path confinement, egress, secrets), the workflow step contract, or the
  import-boundary rules.

A test that exists must catch a real regression, survive refactoring, run fast,
and be cheap to keep. Never test wiring, constants, defaults, struct shapes,
fakes, or call order. One table-driven test per behaviour; extend the table
instead of adding a second test. Prefer making an invalid state unrepresentable
to testing that it is rejected. Test helpers live in `_test.go` files only.

## Invariants

These protect users. Do not weaken them without the maintainer's say-so.

- Failure stays local. Any service may crash, restart or be absent without
  losing accepted work or taking another service down. Callers retry, degrade
  and report the dependency as unhealthy; they never exit because a peer is
  missing, and work is acknowledged only once it is durable.
- The model never runs git. Commits and pushes go through the daemon.
- The daemon's own credentials never enter an agent container. Containers get
  task-scoped grants that are revoked when the task ends.
- Webhook HMAC is verified before the payload is parsed.
- Task briefs go in `<worktree>/.git/task.json`, never the worktree root.
- NATS: task and RPC replies use `msg.Respond`; flush after registering a
  responder; `ARCHIE_TASKS` is a work queue, so never bind overlapping
  consumers.
- `ai-sdk` `FullStream` must be drained completely.

## Build and gate

```bash
task build   # binaries into bin/
task test    # go test -short ./...
task ui      # dashboard into ui/dist
task check   # the gate
```

Requires Go 1.27, Task, golangci-lint, Node/npm and Docker. Adopt `task fmt` and
`go fix` output as-is.

- `task check` gates the commit: `task check && git commit ...`. Never claim a
  gate result you did not just produce on that exact tree; if the gate could not
  run, say what you did verify.
- Commit finished work without asking. Stage only your own paths and commit in
  the same command. Conventional Commits scoped by package, short messages. Push
  only when told.
- Never commit binaries, build output, scratch files or `.references/`. Scratch
  goes in `/tmp`.
- Runtime config lives at `${XDG_CONFIG_HOME:-~/.config}/archie/config.toml`.
  Nothing in the working tree is read at runtime. Systemd is optional.
- Releases: `RELEASING.md`.

## Issues (beads)

Work is tracked in `bd`, never in Markdown TODO files.

```bash
bd ready | bd show <id> | bd update <id> --claim | bd close <id>
```

- An issue is the plan: what is wrong or wanted, and how you know it is done.
  Keep it to that.
- Bead state syncs through Dolt (`bd dolt push/pull`, only when told), not git.
  Never commit `.beads/` exports.
- No host names, IPs, paths, PIDs or credentials in any bead: they are
  published.
- File follow-up work you find as a bead. Do not fix it in passing.

## Changing this file

A rule is added only after a real failure it would have prevented, and it must
say which decision it changes. Remove rules that no longer change decisions.
Keep it short enough to read in full every session.
