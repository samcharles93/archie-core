# Contract conformance audit — decision

**Status:** Implemented as an **advisory** report. Deliberately **not** part of
`task check`.
**Date:** 2026-09-15
**Authority:** sibling to `archie-core-06ag` (standing reachability audit) and
`docs/architecture/dependencies-and-contracts.md`. Replaces neither.
**Open:** withdraw it, or rebuild it structurally. See "Open decision" — not to
be resolved by hardening the regexes.

## Question

Does every declared contract surface have a consumer, or an explicit declaration
that it has none?

Reachability (`tools/reachaudit`) asks the complementary question — *is this code
in a shipped binary?* — and cannot see a capability that is **reachable and
unconsumed**. `ratelimit` was in the binary closure and called by nothing
(`archie-core-6wxb`); reachability would not have found that.

## Decision

Yes, as an advisory survey. `tools/contractaudit` classifies each subject of each
surface:

| Classification | Meaning | Fails `--strict`? |
| --- | --- | --- |
| `CONSUMED` | a call site exercises it | no |
| `DECLARED-UNCONSUMED` | allowlisted in `docs/contract-declarations.json`, with a reason and a tracker | no |
| `UNDECLARED-UNCONSUMED` | nobody reaches it, nobody declared it | **yes** |
| `STALE-DECLARATION` | an allowlist entry that is no longer true | **yes** |

There are **four** classes and **two** of them fail. The allowlist must match
reality exactly, and it fails in both directions: a missing entry is a finding,
and an entry that outlived its subject is a finding, because a stale allowlist is
how a check quietly stops checking anything.

**Not gated.** `task contract` is advisory and `task check` does not run it,
matching `tools/reachaudit`. The surfaces are discovered by scanning source text,
so a rename or a reformat elsewhere in the tree can change the output with nothing
actually wrong. It has already produced 16 false "unconsumed" readings once, when
the dashboard's request helper was renamed. A check that can fail for reasons
unrelated to what it measures must not be able to break a build.

## Surfaces actually built — three, not five

| Surface | Subjects | "Consumed" means |
| --- | --- | --- |
| `proto/state/v1` `StateStoreService` | **45 RPCs** | an adapter body calls `client.<Rpc>(...)` |
| `proto/gateway/v1` `ChatService` | **21 RPCs** | same |
| `internal/webui` dashboard routes | **49 routes** | a path literal appears as a call argument under `ui/src` |

Consumption for the gRPC surfaces is established by **scanning adapter source
text** for calls through the generated client, not by reflection. See the errata
below: the first revision of this document claimed reflection, and the first
implementation did use it, but the shipped mechanism is a regex and the document
was not updated to match.

## What this audit has actually found

**Nothing actionable.** On the current tree it reports 0 undeclared and 0 stale.
Its only allowlisted entries are two RPCs superseded by their streaming variants
(`ListCaptures`, `ListUndispatchedCaptures`), which were already known from the
source comment on `staterpc.Client.ListCaptures`.

Every real defect found in the session that produced this tool was found by other
means:

- the three 415 refusals, by reading `authorizeTaskMutation` and reproducing the
  browser's exact headers against the real handler;
- the dashboard client's duplicated request plumbing, by reading the module.

Recorded plainly because a survey that has never found anything is evidence about
the survey, not about the tree.

## Errata — claims in the first revision of this document that were false

- **"RPC coverage is derived by reflection over the exported client type."**
  It is not. `proto.go` regex-scans adapter bodies for `client.Method(` calls.
  The reflection version was replaced and the document was not updated. The unused
  `google.golang.org/grpc` family in `tools/go.mod` was residue from that
  iteration; `go mod tidy` removed it on 2026-09-15.
- **Surface sizes of "43 / 12 / 52".** The real counts are 45 / 21 / 49.
- **"Three classifications, and the third is the only finding."** Four classes,
  two of which fail.
- **"Raw `fetch()` calls caused 401s to render as `refused`/`broken`."** Wrong.
  Every raw call site passed `res.status` to `ApiError`, so `classifyActionError`
  already mapped 401 to `session-expired`; the chat stream never classified at
  all. The real defect in that area was different (below).
- **A five-surface "Shape" naming `surface.go`, `generated.go`,
  `declarations.go`, and `testdata/`.** None were written. The real files are
  `main.go`, `audit.go`, `proto.go`, `webui.go`.

## The two gaps this was aimed at

**1. `docsgen check` is documented and does not exist.** Still open, filed as
`archie-core-5gzx`. `generated-documentation.md:118-121` advertises
`docsgen data|asyncapi|all|check`; `tools/docsgen/main.go` has exactly two flags
and always writes, so the documented check mode cannot run and line 331's
"generated drift is currently ungated" stands.

**2. The dashboard's client was not centralised while its own docstring claimed
it was.** Now closed by `18e8f8f`. The docstring said "Single place that knows how
to talk to archied… so auth handling and error shape live in one file" while 22
call sites built their own `fetch`, headers, timeout and error handling, and
`chat.jsx` built a 23rd. The defect actually present there was that three
bodyless mutations — `bindingApprove`, `mappingDelete`, `bindingDelete` — omitted
`Content-Type` and were refused **415** on every use, because
`authorizeTaskMutation` requires a JSON media type even when there is no body.
The Go tests missed it because their helper set `Content-Type` unconditionally,
including for a nil body.

## Testing rule — applies to this tool and to anything like it

**Do not assert on the wording, formatting, or content of generated or
hand-authored text and config files.** A test that pins a source snippet, injects
text into a fixture, or counts an identifier in a file is testing that file's
formatting rather than the behaviour under test: an extra space, a newline, a
rename, or a comment that merely mentions the token breaks it, and the
maintenance burden is permanent. A test that cannot tell "the code is wrong" from
"the file is formatted differently" is testing the wrong thing.

Assert behaviour on the observable effect — the request a client makes, the value
a function returns — and keep parsing out of the assertion. Where a parser must
exist, test it through its own interface with in-memory input, not through a file
on disk.

The first revision of this tool violated this in six tests (four that injected
text into fixtures, two that asserted on a source file's contents). All six were
deleted on 2026-09-15. The dashboard suite's "api.jsx holds exactly one fetch
call" check was deleted for the same reason: it broke on a reformat and on a
comment that mentioned `fetch`, and it was patched with a comment-stripper instead
of being removed. Every `api.*` method is now covered behaviourally instead, by
asserting the headers and body each one sends.

## Open decision

1. **Withdraw.** Delete the tool, the allowlist, this document, and the `contract`
   task. It has found nothing, its mechanism is unsound, and the same question is
   better asked by a behavioural test per method.
2. **Rebuild structurally.** Take the contract from the compiler instead of from
   text: the generated `StateStoreServiceClient` / `ChatServiceClient` interfaces
   give the exact RPC set, and `go/ast` over the adapter bodies and
   `internal/webui/server.go` is formatting-insensitive where a regex is not.
   Cost: the dashboard-consumer half has no Go-native JS parser, so it is either
   dropped or replaced by a behavioural test per route — which is what would have
   caught the 415s.

Neither is "make the regexes cleverer". Recorded here rather than acted on
unilaterally because withdrawing a capability and rebuilding one are both larger
decisions than adding a report.

## Acceptance

1. `task contract` prints a report and exits 0.
2. Every unconsumed subject appears with a classification, and where declared with
   a reason and a tracker id.
3. `--strict` fails on an undeclared entry **and** on a stale one. Verified by
   construction against a copy of the tree, not by asserting on the tree's text.
4. The tool neither deletes nor rewrites anything.
5. No test in this tool asserts on the content or formatting of another file.

## Non-goals

- No runtime schema validation of HTTP responses. The webui handlers already
  return non-nil slices on every empty path (`[]SkillView{}`, `[]CuratorView{}`,
  `make([]taskView, len(tasks))`), so the nil-slice class does not apply; adding a
  live-stack validator before it has a defect to find is scaffolding. Deferred,
  and filed as `archie-core-nddv`.
- No re-parse of the `.proto` as a second source of truth; `buf lint` and
  `buf breaking` already own it.
- No generation of `config.example.toml` or `deployments/*.toml` — settled
  against in `config-example-drift.md`.
- No change to `tools/reachaudit`'s scope or report shape.
