# Deep research capability — design

**Status:** Draft for review, not yet decided
**Date:** 2026-09-14
**Scope:** A deep-research capability in archie-core: an orchestrator that
spawns 1..N subagents to explore and research in parallel, collates their
findings, and returns one cited report. Grounded in web search and web fetch.
**Beads issue:** none yet (this document is the input to it)
**Depends on:** nothing shipped for slice 1; slice 2 depends on slice 1.

## §0 Implementation status

Every claim in this document is mapped to what exists in the tree today.

| Claim | Status | Where it stands |
|---|---|---|
| `web_fetch` tool (URL → readable text) | **Implemented** | `internal/tools/webfetch/{tool.go,webfetch.go}`; registered in `internal/app/archied/bootstrap.go` `registerStandaloneTools`, disabled-by-config returns `nil` |
| **Web search** (query → result list) | **Aspirational — zero code** | No search provider, no search tool, no config block anywhere in the tree or in `ai-sdk` v0.1.32 |
| `agent.Subagent` — nested agent in a fresh context window | **Implemented** (dependency) | `github.com/samcharles93/ai-sdk@v0.1.32/agent/subagent.go`. Synchronous, non-streaming, own model/system/toolset, `MaxSteps` defaults to 10, nesting depth capped at 5, returns final text only |
| Subagent used in production | **Implemented, one consumer** | `internal/app/agentworker/review.go` — the adversarial reviewer. That is the only non-test use of `Subagent` in the repo |
| A **delegate/subagent tool** on the chat surface | **Aspirational — zero code** | No `subagent`/`delegate` tool name is registered anywhere |
| **Fan-out orchestration** (N missions, one collator) | **Aspirational — zero code** | `agentloop`'s package doc states it runs *one* mission: "orchestration across missions lives with the caller". No caller does this yet |
| Parallel-safe tool execution | **Aspirational — zero code** | The unreachable `internal/tools/dispatch.go` dispatch/gating engine (`ExecutionClass`, `DispatchSequential`/`DispatchConcurrent`/`ClassifyEntry`) was deleted as dead code (tools-core-1); live concurrency is ai-sdk's `GenerateOptions.MaxParallelToolCalls`, which archie-core never sets |
| Per-result caps + spill-to-disk | **Implemented** | `internal/agentexec/toolset.go` `ToolLimits{MaxResultChars,SpillDir}`, applied in `toolExecute` via `tools.CapPayload` (`limit.go`); no **aggregate** turn cap by design (`toolset.go`). `config.ToolPolicy.SpillDir`; default cap 50k chars, spill dir deliberately not defaulted |
| Tool guardrails (repeat-failure / no-progress) | **Implemented, not on the call path** | `internal/tools/guardrail.go` engine exists. Its only non-test caller is `dispatchOne` — which is itself unreachable — so **the chat path never consults it**, and the agent path only records per *stage* (`domain/workflow/agent.go`) |
| Structured capture tools (model reports into a typed store, not prose) | **Implemented** | `internal/app/agentworker/review.go` `record_finding` / `record_checked` — `core.NewTypedTool` appending to a caller-owned slice. This is the pattern a research source ledger should reuse |
| Curator engine (interval, declared tool set, lifecycle) | **Implemented, tools broken** | `internal/domain/curator`; a curator declaring tools is **refused** at registration (`registry.go`, no `ToolBuilder` implementation) and `curatorLLMRunner` (`internal/app/archied/main.go`) drops `ChatRequest.Tools`. Budgets are time+concurrency only (`runtime.go`) — **no token budget** |
| Cron / scheduling + delivery router | **Implemented, wired into no binary** | `internal/domain/scheduling`, `internal/infrastructure/cronstore` (schema v2, `Kind` discriminator), `internal/infrastructure/crondelivery` — all present and green, but `go list -deps` for every `cmd/` contains none of them, and `cron`-expression firing arithmetic is unimplemented (`schedule.go`, "slice 2"). `ARCHITECTURE.md` still asserts "**No daemon-internal cron.**" |
| Workflow engines (multi-stage, resumable) | **Implemented, worktree-bound** | `internal/domain/workflow` — every non-triage workflow opens with `StagePrepareWorktree()` and mounts `tc.Dir` into a container; the task persists after every stage (`workflow.go`). `triage.go` documents there is "no cheaper directory-less classification primitive" |
| Long-running chat-initiated work with progress | **Partial** | Chat turns run **once, synchronously**, inline in the channel's turn runner (`internal/gateway/turn.go`). Chat can spawn a *workflow task* (`task_spawn`) which is asynchronous — but that path is worktree/PR-shaped |
| **Operator-push delivery of an async result** | **Aspirational — zero code** | Nothing pushes a finished result to a chat. No channel subscribes to the event bus (Telegram only *publishes* `KindTurnCompleted`); `crondelivery.Courier` is the intended seam but has **no implementation and no caller**; `send_file` works only inside the live turn. The one out-of-band notification in the tree is feasibility's n8n webhook (`feasibility.go`) |
| Citation storage / source ledger | **Aspirational — zero code** | No source, citation, or provenance store exists for research output |

> **Target state, not current state.** Everything marked aspirational below is
> design, not a description of the daemon as it runs today.

## Problem

Sam's requirement: *"Deep research should spawn 1 or many subagents for
exploration or research work. And they should collate all findings, they
should use web search/fetch features."*

None of the three primitives that sentence needs can be assembled from what
ships today:

1. **Web search does not exist.** `web_fetch` reads a URL you already have. A
   research loop's first act is to *discover* URLs from a query, and there is
   no tool, provider, or config for that — in archie-core or in the shared SDK.
2. **Nothing can fan out.** `agent.Subagent` can nest *one* nested generation,
   synchronously, and only the PR reviewer calls it. There is no N-way fan-out,
   no bounded concurrency, no shared budget across children, no collation pass.
   `agentloop` explicitly refuses to orchestrate: it runs one mission and the
   caller owns orchestration — and no caller orchestrates.
3. **A research run outlives a chat turn.** A chat turn is synchronous and
   inline: the operator waits, and the turn's own context window is what
   carries the result. A fan-out over N subagents with web search is minutes to
   tens of minutes of wall clock. It cannot be a turn.

The interesting part is not the loop. It is where the boundary sits: which
existing long-running mechanism this reuses, how citations survive collation
when the thing being collated is model-written prose, and what stops a run
from spending unbounded money.

## What the run actually is

Six stages, each of which must be a *stage* rather than a prompt instruction:

1. **Frame.** One cheap model call turns the request into a research brief: the
   question, 3..N distinct sub-questions, and a per-sub-question budget. Output
   is structured (a capture tool), not prose — the fan-out width comes from
   this step, so it must be a number the code reads, not a sentence.
2. **Fan out.** One subagent per sub-question, **in parallel**, with a bounded
   concurrency, each running a read-only research toolset (search + fetch +
   read) against its own fresh context window, each required to record its
   findings as *typed source records* before finishing, and each compressing
   what it returns.
3. **Cross-check.** Deterministic, no model: group by canonical URL and by
   claim, flag single-sourced claims, mark conflicts. See D6.
4. **Collate.** One model pass over the children's *conclusions and source
   records* — never their transcripts — that writes the report body against the
   pre-reconciled source set.
5. **Citation pass.** Its own stage: validate every marker in the body resolves
   to a stored source, and every stored source was fetched successfully.
6. **Render.** The citation list is rendered from the **source store**, not
   from the collator's prose, so a URL can only appear in a report if some
   subagent actually recorded it.

Stages 5 and 6 are the load-bearing decision, and they are what the commercial
products' published failure modes point at: a synthesizer that is free to write
citations will invent them, and a citation that cannot be traced to a fetch is
worse than no citation.

## Prior art: what the disclosed designs and the open-source field say

Two external research passes inform this design — one on the commercial
products (official engineering blogs, model docs, system cards, plus
peer-reviewed benchmarks), one on open-source deep-research implementations
read at pinned commits. The findings that changed decisions:

**Only one fan-out unit buys capacity.** Four different units are in use:
Anthropic fans out *agents with their own context windows*; Google fans out
*queries*; Perplexity fans out *generated programs*; OpenAI uses pipeline-stage
agents for query enrichment only. Query fan-out is cheap and buys breadth of
retrieval; agent fan-out is what buys context capacity. Our step 2 is agent
fan-out, deliberately, because context isolation is the thing a single GPU
cannot otherwise buy.

**Nobody collates section-by-section.** The open-source survey found one merge
prompt over concatenated child summaries (`final_report_generation` in
`langchain-ai/open_deep_research`), lead-agent synthesis (Anthropic), a
code-level reconcile (Perplexity), or single-model continuation. STORM is the
only demonstrated section-mapped merge. This confirms D3: one collation pass,
not a mapped merge.

**Citations are lost at the merge in 8 of 10 surveyed repos.** Only `lajosdeme/mole`
assigns citation markers **mechanically in code before the prompt is built** —
its `internal/output/report.go` carries the comment *"it cannot introduce a
citation, because the citation markers are assigned mechanically before the
prompt is built"* — and stores a verbatim `Quote` plus a `QuoteOffset` (byte
offset into the fetched document) so a citation can be re-verified later.
Claims whose quote is not found in the source are dropped by code
(`internal/actors/mine.go`: `"claim rejected: quote not found in source"`).
This is the strongest available argument for D1, and `QuoteOffset` is worth
adopting: it makes re-verification cheap and turns "the model said so" into
"the document says so, here".

**The failure is measurable and it is at the orchestrator.** An EMNLP 2026
study localising errors across three top-ranked open-source deep-research
systems found **84.7% of final-report errors originate at the orchestrator,
roughly 31% of them hallucinations and the rest citation mistakes**, with
information passing "through agents like a telephone game"
(https://arxiv.org/abs/2608.24306). Anthropic's own mitigations for this are a
separate **CitationAgent** pass after synthesis and a filesystem-artefact
pattern — subagents store work externally and pass back lightweight references
rather than full outputs. Perplexity reached the same conclusion independently.
Both are adopted here: D1 (store, not prose) and a citation pass that is its
own stage.

**A cross-check stage that no vendor has.** Worker A and worker B never compare
notes in any disclosed design, and nothing in the published set *resolves*
conflicts — Perplexity only "notes" them. Grouping findings by URL and by claim,
flagging single-sourced claims, and marking unresolved conflicts is the
cheapest correctness win available and is where a local implementation can beat
the frontier products rather than imitate them.

**Fan-out is expensive in tokens, and on one GPU that is wall clock.** Anthropic
discloses ~15x tokens for multi-agent versus ~4x for a single agent, and
explicit tier rules in the orchestrator prompt (1 agent / 3-10 tool calls;
2-4 subagents / 10-15 calls each; >10 subagents for complex research, 3-5
spawned concurrently). On a single card, N concurrent workers contend for the
same weights and KV budget, so **wall clock scales roughly linearly with worker
count** — the isolation benefit survives, the throughput benefit does not. One
instrumented study puts **web search at 73% of wall-clock time on average (up to
91%)** (https://cseweb.ucsd.edu/~yiying/2025_NIPS_ERW_Deep_Research_Perf_Study.pdf),
which is the argument for fanning out *retrieval* aggressively and *generation*
sparingly, and for overlapping generation with retrieval.

**Frontier citation volume is not a target.** Frontier runs emit 41-113
citation URLs per query, while 3-13% of citation URLs are hallucinated and "more
citations per query do not mean fewer errors per citation"
(https://arxiv.org/abs/2604.03173). Ten to twenty *verified* citations is a
defensible, evidence-backed target.

**Do not port Perplexity's style.** Search-as-code-generation needs
frontier-class codegen against a bespoke SDK and an atomised self-owned search
stack. Port the *intent* — deterministic fan-out, dedupe and filter in code
before anything reaches a model — and leave the code generation to the Go
orchestrator.

## Architecture: three candidate shapes

| # | Shape | Where a run starts | Durability | New code | Cost |
|---|---|---|---|---|---|
| **A** | New capability family `internal/domain/research` + `infrastructure/research`, typed engine, owning registry, lifecycle, background run, completion delivered through an injected courier | A chat tool `research_start` returns a run id | Runs survive the turn; progress observable; resumable | Domain + infra + store + wiring + UI + config | Largest |
| **B** | A fourth cron job kind (`KindResearch`) in `crondelivery`, reusing `scheduling` + `cronstore` + the delivery router | Chat enqueues a one-shot immediate job | Same store and delivery as cron; no new persistence | Runner + kind + blueprint only | Medium — but fakes "on demand" as a scheduled job, and a job store keyed on a schedule is the wrong shape for a request |
| **C** | A `deep_research` **tool** that runs the whole fan-out synchronously inside one chat turn, using `agent.Subagent` for the children | Directly — the model calls the tool | None: the run dies with the turn | Search seam + tool + fan-out helper | Smallest |

**Recommendation: ship C's machinery, then promote it to A.** Slice 2 (below)
builds the orchestrator as a tool because that is the smallest thing that
delivers Sam's stated design and reuses the `Subagent` primitive that already
works. Slice 3 lifts the identical orchestrator behind a durable run once its
runtime cost and shape are known from real use — which is exactly the order
AGENTS.md's solo-project rule asks for ("prefer the smallest workable change"),
and it avoids inventing persistence for a shape that has not been exercised.

Shape B is rejected as the primary path: `cronstore.JobSpec` is a *schedule*
(cron expression, next-run, catch-up); an on-demand research request is not.
Reusing it would mean every ad-hoc research run leaves a permanent job row.

### Where the pieces live (shape C, then A)

| Piece | Home | Contract |
|---|---|---|
| Search provider seam | `internal/domain/research` (interface) + `internal/infrastructure/research/search/<provider>` (impls) | `Searcher.Search(ctx, query, opts) ([]Result, error)`; `Result{URL, Title, Snippet, RetrievedAt}`. Consumer-owned and one-method, per the `crondelivery.Courier` precedent — nothing here talks to a network |
| `web_search` tool | `internal/tools/search` | Mirrors `internal/tools/webfetch`: `Tool(cfg) *tools.ToolEntry`, returns `nil` when disabled, `ClassIdempotent`, `Toolset: "web"` |
| Source store | `internal/domain/research` entity + store | `Source{ID, URL, CanonicalURL, Title, RetrievedAt, Hash, State(ok/failed/duplicate), CitedBy []subq}` |
| Fan-out orchestrator | `internal/domain/research` (pure) with the model/tool access injected | Bounded worker pool over sub-questions; per-child `agent.Subagent`-shaped `Researcher` seam; shared step/token/wall-clock budget |
| Collator | `internal/domain/research` | Model pass over `[]ChildConclusion` + source store → report body |
| Report renderer | `internal/domain/research` | Markdown + numbered citation list rendered **from the store** |
| Chat trigger | slice 2: `internal/tools/research`; slice 3: gateway tool + courier in `internal/app/archied` | — |
| Durable run + delivery (slice 3) | `internal/domain/research` + `internal/infrastructure/research` + `internal/app/archied` wiring | Follows the strict plugin-engine rule in `ARCHITECTURE.md` ("Plugin engine
rule (strict)"): typed contract, owning registry with start/health/stop, narrow typed registrar |

### Why not the curator

The curator family already looks like the right home — a declared interval, a
declared tool set, an owning registry, per-pass budgets and panic isolation.
Two facts rule it out as the host for *this*:

- **Its tool path does not work.** A curator declaring tools is refused
  registration because `ToolBuilder` has no implementation, and even if it were
  bound, `curatorLLMRunner` drops `ChatRequest.Tools` (`main.go`). Fixing
  that is `archie-core-trnd.1`, an independent piece of work.
- **Its contract is maintenance, not requests.** The isolation invariant
  (`docs/architecture/agent-system.md`, "Curator isolation invariant") defines
  curators as periodic peers reasoning over memory. A deep-research run is
  operator-initiated work with a deliverable, on the operator's clock.

A research *curator* is a legitimate later idea — "research this standing topic
weekly" — but it is a consumer of this capability, not its foundation.

## Decisions this design takes

### D1. Citations come from a store, never from prose

Each subagent gets a `record_source` typed tool (the `record_finding` pattern,
`review.go`) whose handler canonicalises the URL, deduplicates against the
store, marks which sub-question recorded it, and appends. The collator may
*select and order* sources; it may not author them. Rendering the reference
list is then a pure function of the store.

The strongest prior art goes further, and one step of it is worth adopting:
store a **verbatim quote plus a byte offset into the fetched document** with
each claim, and drop any claim whose quote cannot be found in the source
(`lajosdeme/mole`: `FindQuote` + `Claim.QuoteOffset`, with the miner logging
`"claim rejected: quote not found in source"`). That converts an unfalsifiable
"the model said so" into a re-checkable "the document says so, at offset N", and
it makes citation re-verification cheap rather than a second research run.

*Rejected alternative:* require the child to end with a JSON block of sources
and parse it. A parse failure loses every source in the child's run, and a
model that reformats its output for readability silently drops citations.

### D2. Fan-out is parallel, bounded, and shares one budget

`agent.Subagent.Run` is synchronous, so N children means N goroutines behind a
semaphore — concurrency from config, sized from the VRAM budget rather than
from the research question. Two reasons to keep it small: the shared model
runtime also serves chat, and on a single GPU N concurrent workers contend for
the same weights and KV budget, so **wall clock scales roughly linearly with
worker count**. The isolation benefit of fan-out survives that; the throughput
benefit does not.

Fan-out width and per-child `MaxSteps` are enforced by the orchestrator, not
requested in a prompt, against a whole-run wall clock and token ceiling.
`agentloop.Budget` already carries `MaxSteps`, `MaxTokens`, `WallClock`, and
`TokenBudgetIs(maxTokens)` exists as a stop condition (`agentloop.go`) —
the shape exists, the fan-out owner does not.

**Fan out retrieval aggressively and generation sparingly.** Web search is 73%
of wall-clock time on average (up to 91%) in the one instrumented study
available, so retrieval is where the parallel win lives; generation is the
serialised, expensive half.

### D3. The orchestrator never holds child transcripts

`Subagent.Run` sends a prompt and returns a string; the parent's history is
never passed down and the child's is never returned. That is the property that
makes N children affordable at all — the parent's context grows by N
conclusions, not N conversations. This is also why collation is a *separate
pass* rather than an accumulating agent loop: the collator is the only place
that must hold all N conclusions, and it holds nothing else.

Each child compresses its own findings before returning, so what crosses the
boundary is a summary, not a transcript. Anthropic names the same pattern
(filesystem artefacts with lightweight references back) and Perplexity arrived
at it independently; it is the one disclosed primitive that *increases*
effective capacity without a bigger context window.

### D4. Search is a provider seam with no silent default

`web_search` is registered only when a provider resolves, following two
existing conventions: return `nil` for a disabled capability
(`webfetch/tool.go` — "an advertised tool that never works costs a round
trip") and resolve the credential *before* registering
(`registerMinimaxTool`, `bootstrap.go`). At least two implementations are
wanted — a self-hosted meta-search endpoint (no per-query cost, no data egress)
and one hosted API (quality fallback) — behind the same one-method interface,
selected by config.

### D5. A run reports what it could not establish

The collator is required to emit, alongside the report, the sub-questions that
returned nothing usable and the sources that failed to fetch. A research report
that omits its own gaps reads as authoritative over a hole, and the operator
cannot tell a negative finding from a missing one.

### D6. Two stages no surveyed design has: a mechanical cross-check and a separate citation pass

Children never compare notes in any disclosed design, and nothing in the
published set *resolves* conflicts — Perplexity only "notes" them. Because
84.7% of final-report errors originate at the orchestrator (mostly citation
errors), both gaps are worth closing in code rather than in a prompt:

- **Cross-check (deterministic, no model).** Group findings by canonical URL and
  by claim. Flag single-sourced claims, mark unresolved conflicts explicitly,
  and count sources per sub-question. This runs *before* the collator, so the
  collator receives a pre-reconciled set rather than raw prose to untangle.
- **Citation pass (its own stage, after collation).** Anthropic runs synthesis
  and then a separate CitationAgent. Here the pass is cheaper for being partly
  mechanical: it validates that every marker in the body resolves to a stored
  source, and that every stored source was actually fetched successfully.

Both stages are small, both are testable without a model, and together they are
where this implementation can be *better* than the frontier products rather
than an imitation of them. A run that is expected to emit 10-20 well-verified
citations rather than 41-113 partly-unverified ones is making a deliberate,
evidence-supported trade.

## Open decisions

1. **Trigger surface.** A chat tool that blocks the turn (shape C) is
   immediately useful but the operator waits; a durable run (shape A) is the
   right end state. Does slice 2 ship the blocking tool, or does the capability
   go straight to durability?
2. **Delivery for a durable run.** Which channel gets the finished report, and
   is the report an inline message, a file, or a served page? The
   `crondelivery.Courier` seam covers the chat case; a long cited report
   probably wants a document, since chat surfaces render markdown but not
   footnotes well.
3. **Search provider choice and egress.** A hosted search API sends every query
   to a third party; a self-hosted meta-search keeps it local. This is an
   operator decision with a real privacy dimension, and the default matters.
4. **Report persistence and reuse.** Does a report become a memory, a document,
   or neither? Reusing a prior report is the difference between "research" and
   "research again next week".
5. **Cost ceiling.** What is the per-run token ceiling, and is it a config
   value, a per-request parameter, or derived from the fan-out width?

## Failure and security paths

- **SSRF.** Search results are attacker-influenceable (a page can rank for a
  query). `web_fetch` already vets the *resolved address* at dial time with
  `AllowPrivateNetworks` off, which is the property to preserve — a search
  tool must not become a way to reach the daemon's own control surfaces by
  returning an internal URL.
- **Prompt injection.** Fetched pages are untrusted content and will contain
  instructions. Subagents fetch; the orchestrator and collator must never
  execute an instruction sourced from a page. Bounded by giving subagents a
  read-only toolset with no shell and no write.
- **Runaway spend.** Unbounded fan-out × unbounded steps is the failure mode.
  Bounded concurrency, per-child step caps, a whole-run wall clock, and a
  token ceiling, all enforced in code.
- **Degenerate fan-out.** A model asked for "N sub-questions" will produce N
  near-duplicates of the same question. Deduplicate sub-questions before
  dispatch, and require distinct search queries per child.
- **Failed dependencies.** Search provider down: the tool is absent (D4), not
  broken. Fetch fails: recorded as a `failed` source so the report can say so.
- **Collator degradation.** If the collator call fails or truncates, the report
  must still be rendered from the source store and the child conclusions —
  degraded, not empty. This mirrors the reviewer's rule that findings captured
  before a failed run are still real (`review.go`).

## Slices

1. **Search seam + `web_search` tool.** Interface, one implementation, config
   block, tool registration, tests. Independently useful to every chat turn.
2. **Fan-out orchestrator + collation, as a tool.** `record_source` capture
   tool (with quote + offset), bounded parallel children with per-child
   compression, cross-check, collator pass, citation pass, store-rendered
   citations. Triggerable from chat; blocks the turn.
3. **Durable run + delivery.** Promote the orchestrator behind the engine
   contract, add the run store, progress events, and completion delivery.
4. **UI + reuse.** Dashboard surface for runs and past reports; optional
   research curator on a schedule.

## Non-goals

- Replacing `web_fetch`; research uses it.
- A general multi-agent collaboration framework (`archie-core-1786637492113-89`
  owns that question).
- Report publication to a static site or external destination.
- Fine-tuning or a dedicated model: the capability is orchestration, and it
  must work on whatever model the operator has configured.
