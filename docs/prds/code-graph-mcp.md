# Code graph over MCP (`tools/codegraph`)

Status: proposed. Supersedes `symbol-reference-index.md`, which scoped only the
data layer.

## The problem

Establishing what a change touches currently costs an agent eight to fifteen
greps, and the answers are wrong in specific ways. `grep -rn "Register("`
cannot separate `plugin.Registry.Register` from `captureintake.Receiver.Register`
from `forgerpc.Server.Register`, all of which exist here. It also matches the
sibling checkouts under `.worktrees/`, which are near-complete copies of the
module, so every count is silently inflated.

That cost lands hardest in investigation, triage and the TDD loop, where the
first question is always "what does this touch".

## Decision: exact analysis, not semantic retrieval

Tools like Greptile parse with tree-sitter, summarise each node, embed the
summaries, and retrieve semantically. That design exists because they must
serve any language. This repository is one Go module, and Go's type checker is
ground truth:

| Question | Source of truth |
| --- | --- |
| Who references this symbol? | `go/packages` + `go/types`, resolved by identity |
| What calls what? | `callgraph` RTA over SSA |
| What imports what? | `go list -deps` |

No vector database, no embedding cost, no edges invented by a summary that
drifted from the code. For a single-language repository this is both cheaper
and strictly more accurate. Copy the interface those tools expose, not their
architecture.

**A code graph does not power a documentation chatbot.** It answers structural
questions about Go symbols. Answering "how do I configure a second identity"
means retrieving prose from markdown, which needs chunking, embedding and
similarity search over a different corpus entirely. That remains a separate
capability with its own design. Conflating the two would build one system that
does neither well.

## Interface: an MCP server

`archied` already consumes MCP as a client (`internal/app/agentworker`
starts providers) and serves none. Exposing the graph over MCP reaches Claude
Code, pi, codex and Archie's own agents through one surface, with no coupling
to the daemon: it is configured like any other entry in `[[mcp_servers]]`.

Four tools, each returning JSON with file:line citations:

- `references(symbol)` — every reference site, classified `prod` / `test` /
  `generated` / `dynamic`
- `callers(symbol, depth)` — inbound call edges
- `callees(symbol, depth)` — outbound call edges
- `blast_radius(file | symbol)` — the transitive neighbourhood a change reaches,
  which is the single call meant to replace the grep sequence

Diagrams are deliberately not a tool. A model given an accurate subgraph writes
its own Mermaid; a diagram endpoint would only fix the rendering of something
the model already has.

## Decisions

**Every answer declares what it could not see.** RTA over-approximates
interface dispatch, and reflection is invisible: every `reflect.ValueOf` entry
in `wfextract`, `gateextract`, `pluginextract`, `secretextract` and
`logextract` makes a symbol look reachable whether or not anything calls it.
Each response carries a `dynamic_edges` field naming those boundaries. An agent
trusting a confident wrong graph is worse off than one that greps, so the graph
states its own limits rather than implying completeness.

**Warm in memory, keyed on HEAD, never persisted to disk.** `deadcode` does
whole-program RTA in under seven seconds; this does less work. The server
rebuilds when HEAD changes and refuses its cache outright when the tree is
dirty. Nothing is written to disk, because a persisted index becomes a rendered
view that outlives its evidence. That is the failure that made `STATUS.md`
report 90 of 91 findings re-verified while most statuses were 77 commits stale,
and the same one that made `reachaudit` serve a previous tree's analysis from
`/tmp` until its cache was keyed on HEAD.

**`tools/codegraph`, not `internal/`.** It is developer tooling, not daemon
runtime, so it belongs beside `docsgen`, `contractaudit` and `reachaudit` in
the `tools` module, which `task test:tools` already covers. Start as a single
Go file; split only when it earns it.

**No new dependency.** `golang.org/x/tools` is already in `go.sum` at v0.49.0.

## How it is verified

Red first. Table-driven tests against small fixture modules built in
`t.TempDir()`, asserting the behaviour that distinguishes this from grep:

1. Two methods spelled `Register` on different types resolve to two separate
   records, and a reference to one does not increment the other.
2. A symbol referenced only from `_test.go` reports zero `prod` references.
3. A symbol referenced only from a `reflect.ValueOf` table reports zero `prod`
   references and one `dynamic`, and names the table in `dynamic_edges`.
4. A generated file's references land in `generated`, not `prod`.

Mutation-verify each: reintroduce the conflated behaviour and watch the
assertion fail.

Acceptance is a live cross-check rather than a unit test. Run it against this
tree and confirm it reports zero production references for
`worktree.Manager.Resume`, `webui.NewDangerousService` and
`secret.DefaultRegistry`. All three were confirmed dead by hand, and `deadcode`
either stayed silent on them or, in the case of a package var, cannot represent
them at all. Agreeing with `deadcode` is not the bar. Catching what `deadcode`
structurally cannot is.
