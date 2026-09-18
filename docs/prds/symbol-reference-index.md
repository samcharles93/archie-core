# Type-resolved symbol reference index (`tools/symbolrefs`)

Status: proposed. Single-file Go tool in the `tools` module.

## The gap

Three questions get asked constantly during audits, and each currently gets
answered by a different unreliable method:

| Question | Today | Problem |
| --- | --- | --- |
| Is this reachable from a shipped binary? | `task deadcode` | Sound but incomplete, functions only |
| Who references this symbol? | `grep` / `ast-grep` | Matches spelling, not identity |
| Is this package in any binary? | `tools/reachaudit` | Package granularity |

The middle row is the hole. `grep -rn "Register("` cannot tell
`plugin.Registry.Register` from `captureintake.Receiver.Register` from
`forgerpc.Server.Register`, all of which exist here. It also matches the three
sibling checkouts under `.worktrees/`, which are near-complete copies of the
module, so every count is inflated by a factor nobody notices.

This matters because reachability alone does not settle a finding. RTA
over-approximates: it assumes every address-taken function may be called, and
every `reflect.ValueOf` entry in `wfextract`, `gateextract`, `pluginextract`,
`secretextract` and `logextract` is address-taken. `deadcode` is therefore
silent on `worktree.Manager.Resume` despite it having zero callers anywhere in
the tree. `deadcode` also reports functions only, so a dead package var like
`secret.DefaultRegistry` is structurally invisible to it.

A reference index answers what reachability cannot: **a symbol with zero
non-test reference sites is dead regardless of what RTA believes about its
address.** The two reports are complementary, and neither subsumes the other.

## What it does

`packages.Load` over the root module with `NeedTypes | NeedTypesInfo |
NeedSyntax | NeedDeps`, then for every identifier in every file resolve
`TypesInfo.Uses[ident]` to a `types.Object` and attribute the reference to its
declaring object. Identity comes from the type checker, so spelling collisions
and sibling worktrees both disappear: `go list` resolves by import path and
never walks `.worktrees/`.

Each reference site is classified by the file that holds it:

- `prod` — an ordinary non-test file in the module
- `test` — `_test.go`
- `generated` — matches the standard generated-file header, plus
  `internal/contracts/**` and `ui/dist/`
- `dynamic` — a `reflect.ValueOf` table or other runtime-loading site, which
  means static analysis cannot see the real caller

`dynamic` is a separate class rather than folded into `prod` because the whole
point is to stop those sites laundering a dead symbol into a live-looking one.

Output is JSONL on stdout, one record per package-level declaration:

```json
{"symbol":"internal/worktree.Manager.Resume","kind":"method",
 "decl":{"file":"internal/worktree/worktree.go","line":261},
 "refs":{"prod":0,"test":4,"generated":0,"dynamic":0},
 "sites":[{"file":"internal/worktree/worktree_test.go","line":88,"class":"test"}]}
```

So the query that took me six hand-run greps this session becomes one line:

```sh
task symbolrefs | jq -c 'select(.refs.prod == 0 and .refs.dynamic == 0)'
```

## Scope

In: package-level funcs, methods, types, vars, consts in the root module.
Out: local variables, struct fields, reachability (that is `task deadcode`),
and any write path. The tool is read-only and advisory. It never proposes
deletion, matching `reachaudit`'s existing stance.

## Decisions

**Single file, `tools/symbolrefs/symbolrefs.go`.** It is dev tooling, not
product code, so it belongs beside `docsgen` and `contractaudit` in the `tools`
module rather than under `internal/`. That module is already covered by
`task test:tools`, which `task check` runs.

**No persisted index.** `deadcode` does whole-program RTA in under 7 seconds;
this does strictly less work (no SSA, no call graph). Anything cached becomes a
rendered view that outlives its evidence, which is the exact failure that made
`STATUS.md` claim 90 of 91 findings re-verified while most statuses were 77
commits stale, and the same reason `reachaudit`'s `/tmp` cache had to be keyed
on HEAD. Recompute; do not reconcile.

**Not in `task check`.** It reports a standing backlog, so gating on it fails
every run until the backlog is zero. Same rationale as `task deadcode` and
`task vuln`.

**No new dependency.** `golang.org/x/tools` is already in `go.sum` at v0.49.0.

## How it is verified

Red first, per the repo protocol. The tests are table-driven against small
fixture modules built in `t.TempDir()`, asserting the classification that
distinguishes this tool from grep:

1. Two distinct methods spelled `Register` on different types resolve to two
   separate records, and a reference to one does not increment the other.
2. A symbol referenced only from `_test.go` reports `prod: 0`.
3. A symbol referenced only from a `reflect.ValueOf` table reports `prod: 0`
   and `dynamic: 1`, not `prod: 1`.
4. A generated file's references land in `generated`, not `prod`.

Mutation-verify each: reintroduce the conflated behaviour and watch the
assertion fail, per `HANDOFF.md` rule 5.

Acceptance is a live cross-check, not a unit test: run it against the tree and
confirm it reports `prod: 0` for `worktree.Manager.Resume`,
`webui.NewDangerousService` and `secret.DefaultRegistry`, all three of which
were confirmed dead by hand this session and all three of which `deadcode`
either missed or could not represent.
