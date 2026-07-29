---
name: archie-failure-archaeology
description: "Reconstruct archie-core failures, dead ends, reversals, rejected fixes, and branch-only repairs from read-only git history and live call sites. Use when a symptom resembles a past production outage, deadlock, retry loop, protocol mismatch, configuration regression, worktree/cache fault, unsafe cleanup, linter semantic change, artifact leak, or recurring baseline repair; also use before reviving an old approach, declaring a fix reverted, or deciding whether a repair is current, settled, conditional, superseded, open, or merely a candidate."
---

# Archie failure archaeology

Stop the project from paying twice for the same lesson. Reconstruct what
happened from checked-out code, tests, configuration, and commit graph.
Treat commit prose as a lead, not proof. Preserve uncertainty.

All volatile observations are dated **2026-07-28**.

Route to `archie-debugging-playbook` for active symptoms,
`archie-codebase-discovery` for deep caller tracing.

## Use the status taxonomy exactly

| Status | Meaning |
|---|---|
| `settled` (`fixed`) | The failure, cause, resolution, and live replacement are proven. |
| `current` | The checked-out authoritative path still contains the behavior or contradiction. |
| `open` | The defect is evidenced, but no accepted and integrated resolution. |
| `conditional` | A fix is proven only for the named configuration, execution mode, peer, or assembly. |
| `superseded` | A mechanism was intentionally replaced. |
| `candidate` | A branch, proposal, or partial implementation may help but is not accepted. |

Never use `settled` to mean "a fix commit exists."

## Run the archaeology workflow

### 1. Freeze the observation point

```sh
git rev-parse HEAD
git status --short
git log -1 --date=iso-strict --format='%H %ad %s'
git branch -a --no-color --no-merged HEAD
```

### 2. Name the exact symptom

Capture: first wrong observable result; last confirmed-good boundary; process
mode; active config and deployment assembly; whether deterministic,
load-dependent, or production-only.

### 3. Trace current behavior before history

```sh
rg -n '<symptom|symbol|setting|error>' cmd internal config*.toml docker-compose.yml docs
git log --all --date=iso-strict --format='%H %ad %s' -- <affected-paths>
git blame -L <start>,<end> -- <live-file>
```

### 4. Prove the historical transition

```sh
git show --no-ext-diff --format=fuller --stat <commit>
git show --no-ext-diff -U80 <commit> -- <affected-paths>
git merge-base --is-ancestor <commit> HEAD
git branch -a --contains <commit>
```

Read parent version when failure is semantic:

```sh
git show <commit>^:<path>
git show <commit>:<path>
```

Require all six fields in an incident packet:

| Field | Required content |
|---|---|
| Symptom | Operator, user, test, or protocol observation |
| Attempted solution | What was tried before or during the failure |
| Root cause | The violated contract or mistaken assumption |
| Evidence | Commit, live path, test, trace, or production observation |
| Current status | One taxonomy value, dated when volatile |
| Settled rule | A constraint future work must preserve |

### 5. Reproduce narrowly, then route

```sh
env GOCACHE=/tmp/archie-archaeology-gocache GOTMPDIR=/tmp go test ./internal/<package> -run '^TestName$' -count=1 -v
```

If current behavior differs from chronicle, update the packet.

## Match symptoms to known incidents

| Symptom | Start with |
|---|---|
| `nats: no responders available for request` only from agent containers | Container network incident |
| Daemon crash-loops after config/schema work | SecretRef compatibility and Compose passthrough incidents |
| Stream shows empty cursor or first token, then hangs | FullStream/TextStream incident |
| MCP unit tests pass but real SDK server hangs | MCP framing and mirrored-peer incident |
| Telegram shows old commands after menu update | Telegram server-side scope incident |
| Tool discovery deadlocks, aliases mutate, or bad plugin hides rest | Tool-registry adversarial findings |
| Hidden `<memory>` content appears at end of truncated stream | Scrubber `Flush` leak |
| Broad lint cleanup compiles poorly while tests pass | Autofix, `nilerr`, and `308c199` incidents |
| Reused worktree is stale, half-prepared, or coupled to expired objects | Worktree/askpass/cache history |
| Repeated `fix: baseline gate repair` commits or parked tasks cycle | Baseline, no-op, and retry-loop incidents |
| CI cannot resolve `/work/apps/...` | Local module replacement incident |
| Build caches or dependency trees appear in diff | `.gotmp` and `node_modules` incidents |
| `diff_cap_lines = 0` still behaves as 400 | Diff-cap zero contradiction |

## Chronicle: work intake and retry loops

| Incident and date | Symptom | Root cause | Evidence and status | Settled rule |
|---|---|---|---|---|
| Label auto-claim, 2026-07-22 | >80 labelled issues enrolled under label polling. | Label polling means eligibility, not hint. | `519882f` changes `config.docker.toml` to `assignee`. `internal/config/config.go` defaults to `assignee` but permits `label` and `either`. **Status:** `settled`. | Default to assignee-driven pickup. Label/either are deliberate bulk enrollment. |
| Baseline-red park storm, 2026-07-23 onward | Task parks on gate failure that existed before its feature work. | Parking/retrying cannot change pre-existing failure; auto-repair mixes repository repair into feature scope. | `b2482af` made `StageBaselineGate` launch builder instead of parking. Nine repair commits in history. **Status:** `current` mitigation and architecture weak point. | Measure untouched baseline first. Track baseline repair as explicit change. |
| Passed build with zero changes, 2026-07-23 | Commit-push reports "no changes," parks, retries, repeats. | "Passed with no changes" lacked terminal domain outcome. | `62c0df1` introduced no-op detection; `2bf42bd` set `Outcome`. `TestStageCommitPushClosesIssueWhenBuildNoChanges`. **Status:** `settled`. | Model no-op as explicit terminal result. |
| Infinite parked-task retry, 2026-07-23 | Removing parked label repeatedly requeues with no terminal bound. | Retry count not durable or capped. | `a8fdb50` added persisted `retry_count`, global/per-repo `max_retries`, and `dead`. **Status:** `settled`. | Every autonomous retry needs durable attempt state, explicit bound, terminal state, and operator-visible reason. |

## Chronicle: runtime and peer contracts

| Incident and date | Symptom | Root cause | Evidence and status | Settled rule |
|---|---|---|---|---|
| Container network auto-detection, 2026-07-27 | Every container task returns `nats: no responders`. | `selfNetwork` inspected hostnamed container, silently fell back to default bridge. | `37cf089` records incident, adds `containers.network`; `2ac306b` pins `archie-core_default`. **Status:** `conditional`. | Make cross-container network membership explicit in deployment. |
| SecretRef schema crash-loop, 2026-07-27 and current `HEAD` | Both identities demand `ARCHIE_GITHUB_TOKEN` despite Gitea config. | Removing old `token_env` field let TOML ignore deployed value. `1f8588d` restored precedence-compatible fallback; `308c199` removed `Forge.TokenEnv` declaration while `finalize` still references it. **Status:** `open` regression at `HEAD`. | Preserve decoded compatibility until deployed files migrated and rejection of stale keys observable. |
| Compose secret omission, 2026-07-27 | Config names valid env var, container sees it empty. | Compose forwards only listed environment entries. | `22fc0ef`, `d0624b7`. **Status:** `conditional`. | Trace every secret from config key to host value to Compose passthrough to process lookup. |
| FullStream/TextStream deadlock, 2026-07-27 | Streamed Telegram reply renders empty cursor, typing never clears. | ai-sdk makes `FullStream` authoritative and backpressured; `TextStream` is best-effort. | `6a0bd27`. **Status:** `settled` with regression-test gap. | Drain authoritative stream through close. |
| MCP Content-Length framing and mirrored fake, 2026-07-26 to 2026-07-27 | Internal MCP tests pass while official-SDK servers do not. | MCP stdio uses newline-delimited JSON. Test subprocess called same helpers, mirroring the same wrong protocol. | Compare `f2768eb:internal/tools/mcp/framing.go` with `dda4cde`. **Status:** `settled`. | For wire contracts, test raw canonical frames and independently implemented peer. |
| Telegram command scopes persist by bot token, 2026-07-27 | Startup reports eight commands published, DMs show ~60 old Hermes commands. | Telegram chooses most specific scope; older scopes survive redeploys. | `cec0f3e` adds menu and deny-by-default; `576c7f5` publishes all three scopes. **Status:** `settled`. | Inventory and overwrite every relevant server-side scope. |
| NATS fetch error looked like empty queue, 2026-07-27 | Consumer receives zero messages, treats JetStream failure as "no work." | Batch error state not checked after channel closed. | `0d09a2e` repro; `78a9145` merged through `21ceef0`. **Status:** `settled`. | Check terminal iterator/batch error state even when data channel is empty. |

## Chronicle: implementation and tooling traps

| Incident and date | Symptom | Root cause | Status |
|---|---|---|---|
| Tool registry passed first implementation but held eight defects, 2026-07-26 | Availability can deadlock; schemas mutate after registration; discovery stops at first bad source; literal extraction wrong. | Callbacks ran under `RLock`, copies shallow/shared, parsing partial, matching broad, discovery fail-fast. | `9f6c36c` lists all eight fixes. **Settled.** Never call extension code under registry lock. Clone nested data. Parse complete grammar. Isolate discovery failures. |
| Memory scrubber `Flush` leak, 2026-07-26 | Stream ending inside `...secret</mem` emits buffered hidden memory to visible response. | Pending bytes did not retain whether captured inside `<memory>` block. | `b4aaaf0`. **Settled.** Test every streaming state at EOF. Flush is security boundary. |
| Linter autofix shadowing, 2026-07-28 | 24-file "style" pass stops compiling. | `gocritic` changed `ctx, cancel = context.WithTimeout(...)` to `:=` inside conditional, shadowing context. | `9b44bac`. **Settled.** Treat autofix output as authored behavior. |
| All eleven `nilerr` findings semantic false positives, 2026-07-28 | Mechanical "fixes" would return Go errors while tests pass, breaking model correction, chat, NATS envelopes, clean shutdown, indexing. | Linter cannot see failures carried as text, envelopes, shutdown signals, or skipped entries. | `86cfc19`. **Settled.** Review semantic channels one site at a time. |
| NellDB local replace broke delivery, 2026-07-26 to 2026-07-27 | Every CI Docker build fails `go mod download` on `/work/apps/nell-engine`. | Local `replace` paths don't exist in CI/build contexts. | `07fb291` removes replace. **Settled.** Never commit machine-local `replace` directives. |
| Broad checkpoint hid unresolved merge conflict, 2026-07-27 | Literal conflict marker reached committed source. | Unrelated surfaces combined without whole-diff conflict scan. | `4429ae5`. **Settled.** Keep commits cohesive. |

### Preserve the eight tool-registry review lessons

| Defect fixed in `9f6c36c` | Required review question |
|---|---|
| `Available` evaluated `CheckFn` under `RLock` | Can a callback re-enter or block while lock is held? |
| `Register` stored caller references | Can caller mutate registered state afterward? |
| `Clone` copied nested schema maps/slices shallowly | Is every mutable layer detached? |
| `stringLit` mishandled escapes/backticks | Does parsing follow Go literal semantics? |
| `intLit` omitted negative/hex/octal/binary forms | Are all supported numeric forms handled or rejected? |
| `Discover` aborted on first error | Can one bad extension hide healthy extensions? |
| `isRegisterCall` matched too broadly | Is the exact receiver/call shape proven? |
| `schemaLit` accepted only string values | Are booleans, integers, arrays, nested maps preserved? |

## Chronicle: worktree and repository state reversals

| Step and date | Symptom | Root cause | Settled rule |
|---|---|---|---|
| Existing worktree skipped, 2026-07-23 | `Prepare` returned when `.git` existed. | Existence did not prove freshness or completed preparation. Later replaced `.git` readiness with sentinel. | Define completed-state marker and revalidate remote/base/branch state on reuse. |
| Sentinel outside `.git`, 2026-07-24 | `.archie-prepared` marked setup; go-git `Add(All)` doesn't honor exclude. | Orchestration metadata needs to be outside tracked worktree by construction. | Put sentinel at `.git/archie-prepared`. |
| Askpass rewritten each call, 2026-07-24 | Token-emitting shell script rewritten for every git command. | Comment wasn't invariant; no test checked mtime. Later removed shell git and askpass entirely. | Prefer in-process credentials. |
| Bare object cache, 2026-07-24 | `--reference-if-able --dissociate` plus TTL to accelerate clones. | go-git has no `--dissociate` equivalent; expiring cache would corrupt active worktrees. | Never accept clone acceleration making task correctness depend on expiring cache. |
| Shell git replaced by go-git, 2026-07-28 | Port exposed host GPG inheritance, sentinel tracking, merge-base diff semantics. | RPC handlers used `context.Background`, making in-process clones unbounded. | Audit every adapter and cancellation boundary after replacing killable subprocess with in-process I/O. |

## Chronicle: cleanup, artifacts, and contradictory policy

| Incident and date | Symptom | Root cause | Status | Settled rule |
|---|---|---|---|---|
| Tracked `.gotmp`, 2026-07-27 | Build-cache binaries, generated `_testmain.go`, `.git-askpass` appear in commits. | Workaround path not ignored before use. | `4cb0577` removes artifacts and adds `.gotmp/` to `.gitignore`. **Settled.** | Put caches outside repo or ignore before first command. |
| Tracked `docs/node_modules`, 2026-07-28 | Hundreds of pnpm symlink entries become repository content. | `docs/node_modules` neither ignored nor excluded from commit. | 237 entries at `HEAD`; no `.gitignore` rule. **Current/open.** | Track lockfiles, never installed trees. |
| `308c199` cleanup regression, 2026-07-28 | `TestRunWrapsExternalCommand` returns `"\n"` instead of `"wrapped\n"`; forge compatibility removed again; dependency artifacts added. | Semantic edits classified and reviewed as cleanup. | Focused skillscript test fails at `HEAD`. **Current/open.** | No commit is "cleanup" for gating purposes. |
| `diff_cap_lines = 0` contradiction, introduced at project start | Operators set zero expecting unlimited, but decoded config becomes 400. | `finalize` converts zero to default 400; same sentinel means both "unset" and "unlimited." | **Current/open** contradiction. | Never overload zero value with both defaulting and explicit policy. |

## Keep unmerged work as dated candidates

As of **2026-07-28**, these remote branches are not merged:

| Candidate branch | Claimed concern |
|---|---|
| `origin/fix/43-loaddir-no-timeout-around-yaegi-eval-can-hang-daemon-startup` | Bound Yaegi evaluation during `LoadDir` |
| `origin/fix/45-skillscriptrun-ignores-context-cannot-be-canceledtimed-out` | Make skill scripts cancellable |
| `origin/fix/48-transition-ignores-from-status-guard-allowing-lost-updates` | Enforce expected source state atomically |
| `origin/fix/52-handlesse-silently-swallows-eventssince-backlog-fetch-errors` | Surface SSE backlog query errors |
| `origin/feat/51-no-test-coverage-for-stagestats-query` | Add `StageStats` query coverage |
| `origin/feat/75-add-mcp-client-capabilities-to-archied-and-archie-agent` | Wire MCP through both execution modes |

Verify candidate state:

```sh
git branch -r --no-merged HEAD
git log -1 --date=iso-strict --format='%H %ad %s' <branch>
git diff --stat HEAD...<branch>
git merge-base --is-ancestor <branch> HEAD
```
