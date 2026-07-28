---
name: archie-failure-archaeology
description: "Reconstruct archie-core failures, dead ends, reversals, rejected fixes, and branch-only repairs from read-only git history and live call sites. Use when a symptom resembles a past production outage, deadlock, retry loop, protocol mismatch, configuration regression, worktree/cache fault, unsafe cleanup, linter semantic change, artifact leak, or recurring baseline repair; also use before reviving an old approach, declaring a fix reverted, or deciding whether a repair is current, settled, conditional, superseded, open, or merely a candidate."
---

# Archie failure archaeology

Stop the project from paying twice for the same lesson. Reconstruct what
happened from the checked-out code, tests, configuration, and commit graph.
Treat commit prose as a lead, not proof. Preserve uncertainty.

All volatile observations below are dated **2026-07-28**. Re-run the
provenance commands before relying on a status.

## Route the work

| Need | Load instead or alongside |
|---|---|
| Triage an active symptom and choose the next diagnostic | `archie-debugging-playbook` |
| Decide ownership, boundaries, migration, or a replacement design | `archie-architecture-planning-campaign` |
| Accept, gate, review, or close a change | `archie-change-control` |
| Traverse callers, ASTs, duplicate paths, or apparently dead code deeply | `archie-codebase-discovery` |

Do **not** use this skill:

- as permission to change code, configuration, git state, or production;
- as a substitute for reproducing a current failure;
- to promote a commit subject, old PRD, Beads record, or remote branch into
  current truth;
- to call work “reverted” unless a revert commit or an inverse diff proves it;
- to design the next architecture. Carry proven constraints into
  `archie-architecture-planning-campaign`.

## Use the status taxonomy exactly

| Status | Meaning |
|---|---|
| `settled` (`fixed`) | The failure, cause, resolution, and live replacement are proven. Preserve the resulting rule. |
| `current` | The checked-out authoritative path still contains the behavior or contradiction. |
| `open` | The defect is evidenced, but no accepted and integrated resolution is proven. |
| `conditional` | A fix is proven only for the named configuration, execution mode, peer, or deployment assembly. |
| `superseded` | A mechanism was intentionally replaced. Study it for constraints; do not restore it by default. |
| `candidate` | A branch, proposal, or partial implementation may help, but is not accepted current behavior. |

Never use `settled` to mean “a fix commit exists.” Prove that the fix is an
ancestor of the checkout and that live composition still selects it.

## Run the archaeology workflow

### 1. Freeze the observation point

Run only read-only git commands:

```sh
git rev-parse HEAD
git status --short
git log -1 --date=iso-strict --format='%H %ad %s'
git branch -a --no-color --no-merged HEAD
```

Record the commit and dirty paths. A dirty live file may contain a repair that
`HEAD` does not. Report both; do not silently choose one.

### 2. Name the exact symptom

Capture:

- the first wrong observable result;
- the last confirmed-good boundary;
- process mode (`inprocess`, `subprocess`, or NATS/container);
- the active config and deployment assembly;
- whether the failure is deterministic, load-dependent, or production-only.

Do not start with a preferred fix.

### 3. Trace current behavior before history

```sh
rg -n '<symptom|symbol|setting|error>' cmd internal config*.toml docker-compose.yml docs
git log --all --date=iso-strict --format='%H %ad %s' -- <affected-paths>
git blame -L <start>,<end> -- <live-file>
```

Trace entry point, caller, owner, adapter, peer, persistence, config decoder,
environment passthrough, and tests. Use `archie-codebase-discovery` when that
cone is not obvious.

### 4. Prove the historical transition

```sh
git show --no-ext-diff --format=fuller --stat <commit>
git show --no-ext-diff -U80 <commit> -- <affected-paths>
git merge-base --is-ancestor <commit> HEAD
git branch -a --contains <commit>
```

Read the parent version when the failure is semantic:

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

Use a focused, non-mutating check where practical. Put Go caches in `/tmp`:

```sh
env GOCACHE=/tmp/archie-archaeology-gocache GOTMPDIR=/tmp go test ./internal/<package> -run '^TestName$' -count=1 -v
```

If current behavior differs from the chronicle, update the packet rather than
forcing the symptom into an old story. Route a proposed correction through
`archie-change-control`.

## Match symptoms to known incidents

| Symptom | Start with |
|---|---|
| `nats: no responders available for request` only from agent containers | Container network incident |
| Daemon crash-loops after config/schema work | SecretRef compatibility and Compose passthrough incidents |
| Stream shows an empty cursor or first token, then hangs | FullStream/TextStream incident |
| MCP unit tests pass but a real SDK server hangs or rejects input | MCP framing and mirrored-peer incident |
| Telegram shows old commands after a successful menu update | Telegram server-side scope incident |
| Tool discovery deadlocks, aliases mutate, or one bad plugin hides the rest | Tool-registry adversarial findings |
| Hidden `<memory>` content appears at end of a truncated stream | Scrubber `Flush` leak |
| Broad lint cleanup compiles poorly or changes semantics while tests pass | Autofix, `nilerr`, and `308c199` incidents |
| A reused worktree is stale, half-prepared, or coupled to expired objects | Worktree/askpass/cache history |
| Repeated `fix: baseline gate repair` commits or parked tasks cycle | Baseline, no-op, and retry-loop incidents |
| CI cannot resolve `/work/apps/...` | Local module replacement incident |
| Build caches or dependency trees appear in the diff | `.gotmp` and `node_modules` incidents |
| `diff_cap_lines = 0` still behaves as 400 | Diff-cap zero contradiction |

## Chronicle: work intake and retry loops

| Incident and date | Symptom | Attempted solution and root cause | Evidence and status | Settled rule |
|---|---|---|---|---|
| Label auto-claim, 2026-07-22 | More than 80 labelled issues were enrolled when the deployment used label polling. | **Attempt:** use the `archie` label as the work trigger. **Cause:** label polling means eligibility, not a hint; it selected every matching issue without explicit assignment. | `519882f` changes `config.docker.toml` from `label` to `assignee`; `docs/archive/decisions.md` records the 80+ issue incident. `internal/config/config.go` now defaults to `assignee` but still permits explicit `label` and `either`. **Status:** `settled`. | Default to assignee-driven pickup. Treat `label` or `either` as deliberate bulk enrollment and review their full candidate set first. |
| Baseline-red park storm and repair churn, 2026-07-23 onward | A task repeatedly parks on a gate failure that existed before its feature work. History then accumulates unrelated baseline-repair commits. | **Attempt:** `b2482af` made `StageBaselineGate` launch the builder to repair a red baseline instead of parking. **Cause:** parking and retrying cannot change a pre-existing failure; auto-repair avoids that loop but mixes repository repair into feature scope. | `internal/workflow/implement.go`; `docs/archive/decisions.md`; `git log --all --grep='baseline gate repair'` returns nine repair commits in the recorded history. **Status:** `current` mitigation and architecture weak point. | Measure the untouched baseline first. Track baseline repair as an explicit change with its own evidence; never attribute its diff or success to the requested feature. |
| Passed build with zero changes, 2026-07-23 | Commit-push reports “worktree has no changes,” parks, retries, and repeats even though the requested fix already exists. | **Attempt:** `62c0df1` introduced no-op detection; it was incomplete until `2bf42bd` set an `Outcome` so the workflow actually stopped. **Cause:** “passed with no changes” lacked a terminal domain outcome. | `TaskContext.BuildNoChanges`, build `OnResult`, and `StageCommitPush` in `internal/workflow`; `TestStageCommitPushClosesIssueWhenBuildNoChanges`. **Status:** `settled`. | Model no-op as an explicit terminal result. Test both the side effect and workflow termination. |
| Infinite parked-task retry, 2026-07-23 | Removing the parked label repeatedly requeues a task with no terminal bound. | **Attempt:** requeue on label removal. **Cause:** retry count was not durable or capped. `a8fdb50` added persisted `retry_count`, global/per-repo `max_retries`, and `dead`. | `internal/daemon/daemon.go` (`maybeRetryParked`, `markDead`), `internal/store` retry tests, `config.example.toml`. Default is three in `finalize`. **Status:** `settled`. | Every autonomous retry needs durable attempt state, an explicit bound, a terminal state, and an operator-visible reason. |

## Chronicle: runtime and peer contracts

| Incident and date | Symptom | Attempted solution and root cause | Evidence and status | Settled rule |
|---|---|---|---|---|
| Container network auto-detection, 2026-07-27 | Every container-mode task returns `nats: no responders available for request`; workers never subscribe. | **Attempt:** `selfNetwork` inspected a container named after `os.Hostname` and silently fell back to the default bridge. **Cause:** the production hostname/container identity did not resolve to the running daemon, so spawned workers could not resolve `nats`. | `37cf089` records the production incident and adds `containers.network`; `2ac306b` pins `archie-core_default`; live `config.docker.toml` remains explicit and `internal/container/pool.go` warns on fallback. **Status:** `conditional`: fixed for the tracked Docker overlay; auto-detection remains a fallback elsewhere. | Make cross-container network membership explicit in deployment. Keep auto-detection diagnostic fallback only. |
| SecretRef schema crash-loop, 2026-07-27 and again at current `HEAD` | Both production identities demand `ARCHIE_GITHUB_TOKEN` despite Gitea config, then crash-loop. | **Attempt:** replace flat `[forge].token_env` with `Forge.Token SecretRef`. **Cause:** removing the old field let TOML silently ignore deployed `token_env`; defaulting then selected the wrong secret. `1f8588d` restored precedence-compatible fallback. | `1f8588d` and `internal/config/config_test.go` document the outage. `308c199` removes the `Forge.TokenEnv` declaration while `finalize` still references it, so that commit's tree does not compile. As of 2026-07-28 the dirty worktree reintroduces the field, but `HEAD` does not. **Status:** `open` regression at `HEAD`; reverify. | Preserve decoded compatibility until deployed files are migrated and rejection of stale keys is observable. Never rely on permissive decoding. |
| Repeated Compose secret omission, 2026-07-27 | Config names a valid env var, but the container sees it as empty and exits. | **Attempt:** define variables in the host environment/config only. **Cause:** Compose forwards only listed environment entries. `ARCHIE_GITEA_TOKEN_ARCHIE` and later `HEYARCHIE_TELEGRAM_BOT_TOKEN` were each omitted. | `22fc0ef`, `d0624b7`, live `docker-compose.yml`, and token resolution in `cmd/archied/main.go`. **Status:** `conditional`: fixed for these exact names in the tracked Compose assembly. | Trace every secret from config key to host value to Compose passthrough to process lookup. Add an assembly test or checklist whenever a secret name changes. |
| FullStream/TextStream deadlock, 2026-07-27 | A streamed Telegram reply renders an empty cursor or first token, typing never clears, and the turn never completes. | **Attempt:** reconstruct replies from `TextStream`. **Cause:** ai-sdk makes `FullStream` authoritative and synchronously backpressured; leaving it unread blocks the producer. `TextStream` is best-effort and may drop deltas. | `6a0bd27` and the live loop in `cmd/archied/main.go`; current gateway tests exercise routing but do not directly recreate ai-sdk channel backpressure. **Status:** `settled` implementation with a regression-test gap. | Drain the authoritative stream through close, derive text from its typed parts, then inspect terminal error state. Do not validate with a convenience projection alone. |
| MCP Content-Length framing and mirrored fake, 2026-07-26 to 2026-07-27 | Internal MCP tests pass while official-SDK servers do not understand the client. | **Attempt:** `f2768eb` implemented LSP-style `Content-Length`; `3ad57c9` strengthened that implementation. **Cause:** MCP stdio uses newline-delimited JSON. The test subprocess called the same `readMessage` and `writeMessage` helpers as the client, so both sides agreed on the same wrong protocol. | Compare `f2768eb:internal/tools/mcp/framing.go` with `dda4cde`; inspect `testMCPServer.serve` in `transport_test.go`; live opt-in independent peer is `TestDesktopCommanderClientCompatibility`. **Status:** `settled`. | For wire contracts, test raw canonical frames and an independently implemented peer. A mirrored fake proves internal symmetry, not interoperability. |
| Telegram command scopes persist by bot token, 2026-07-27 | Startup reports eight commands published, but DMs still show roughly 60 old Hermes commands. | **Attempt:** call `SetMyCommands` for only the default scope. **Cause:** Telegram chooses the most specific scope, and older `all_private_chats`/`all_group_chats` state survives redeploys because it is server-side and keyed to the token. | `cec0f3e` adds the menu and deny-by-default sender allowlist; `576c7f5` records `getMyCommands` evidence and publishes all three scopes. Live command specs are centralized in `internal/channels/telegram`. **Status:** `settled`. | Inventory and overwrite every relevant server-side scope. A new process or code deploy does not reset remote token state. Authorize by sender identity, not group membership. |
| NATS fetch error looked like an empty queue, 2026-07-27 | A consumer receives zero messages and treats a JetStream failure as “no work.” | **Attempt:** infer success from the message count alone. **Cause:** batch error state was not checked after the channel closed. | `0d09a2e` is the repro; `78a9145` is merged through `21ceef0`; `internal/nats/client_test.go` constructs zero messages plus a batch error. **Status:** `settled`. | Check terminal iterator/batch error state even when the data channel is empty. Empty is data, not proof of success. |

## Chronicle: implementation and tooling traps

| Incident and date | Symptom | Attempted solution and root cause | Evidence and status | Settled rule |
|---|---|---|---|---|
| Tool registry passed its first implementation but held eight defects, 2026-07-26 | Availability can deadlock; caller-owned schemas mutate after registration; discovery stops at the first bad source; Go-literal extraction accepts the wrong calls or loses values. | **Attempt:** implement the happy-path registry and source extractor. **Cause:** callbacks ran under `RLock`, copies were shallow/shared, literal parsing was partial, matching was broad, and discovery was fail-fast. | `9f6c36c` lists all eight fixes and regression tests in `internal/tools/registry_test.go` and `toolentry_test.go`. **Status:** `settled`. | Never call extension code under a registry lock. Clone nested caller data. Parse the complete supported literal grammar. Isolate discovery failures and match only the intended registration shape. |
| Memory scrubber `Flush` leak, 2026-07-26 | A stream ending inside `...secret</mem` emits buffered hidden memory text to the visible response. | **Attempt:** flush every unresolved partial tag as literal text. **Cause:** pending bytes did not retain whether they were captured inside a `<memory>` block. | `b4aaaf0`, `internal/memory/scrubber/scrubber.go`, and truncated-close-tag tests. Found by adversarial review. **Status:** `settled`; implementation has since been simplified, so re-run tests before changing it. | Test every streaming state at EOF: outside block, inside block, nested block, and every partial open/close-tag prefix. Flush is a security boundary. |
| Linter autofix shadowing, 2026-07-28 | A 24-file “style” pass stops compiling; one token away, a prefetch timeout would silently stop applying. | **Attempt:** run `golangci-lint --fix` plus formatters broadly. **Cause:** `gocritic` changed `ctx, cancel = context.WithTimeout(...)` to `:=` inside the conditional, shadowing the context. | `9b44bac`; live assignment in `internal/memory/manager.go`. **Status:** `settled` incident. | Treat autofix output as authored behavior. Inspect every semantic diff and preserve assignment-versus-declaration intent explicitly. |
| All eleven `nilerr` findings were semantic false positives, 2026-07-28 | Mechanical “fixes” would return Go errors while tests could still pass, breaking model correction, chat usage feedback, NATS envelopes, clean shutdown, and best-effort indexing. | **Attempt:** classify all `nilerr` reports as bug candidates. **Cause:** the linter cannot see that failures are deliberately carried as text, envelopes, shutdown signals, or skipped entries. | `86cfc19` documents each site; live `//nolint:nilerr` comments state local intent. **Status:** `settled`. | Review semantic channels one site at a time. Suppress locally with the reason. Never fan out a rule-name cleanup without contract analysis. |
| NellDB local replace broke delivery, 2026-07-26 to 2026-07-27 | Every CI Docker build fails `go mod download` on `/work/apps/nell-engine`, leaving production on an older image. | **Attempt:** point `github.com/samcharles93/NellDB v0.2.5` at a developer checkout in `d9d4822`. **Cause:** local module paths do not exist in CI/build contexts. | `07fb291` removes the replace after comparing local HEAD with published v0.2.5; live `go.mod` has the module and no replace. **Status:** `settled`. | Never commit machine-local `replace` directives. Prove the published version, checksum resolution, and container build path. |
| Broad checkpoint hid an unresolved merge conflict, 2026-07-27 | A literal conflict marker reached committed source amid many unrelated epics. | **Attempt:** checkpoint broad concurrent in-flight work to avoid loss. **Cause:** unrelated surfaces were combined without a whole-diff conflict scan. | Commit message for `4429ae5` states that it fixed a previously committed conflict in `internal/memory/scrubber/scrubber.go`. **Status:** `settled` incident. | Keep commits cohesive. Before any checkpoint, scan the exact diff for conflict markers and run affected package tests. |

### Preserve the eight tool-registry review lessons

| Defect fixed in `9f6c36c` | Required review question |
|---|---|
| `Available` evaluated `CheckFn` under `RLock` | Can a callback re-enter or block while a lock is held? |
| `Register` stored caller references | Can the caller mutate registered state afterward? |
| `Clone` copied nested schema maps/slices shallowly | Is every mutable layer detached? |
| `stringLit` mishandled escapes/backticks | Does parsing follow Go literal semantics? |
| `intLit` omitted negative/hex/octal/binary forms | Are all supported numeric forms handled or rejected explicitly? |
| `Discover` aborted on its first error | Can one bad extension hide healthy extensions? |
| `isRegisterCall` matched too broadly | Is the exact receiver/call shape proven? |
| `schemaLit` accepted only string values | Are booleans, integers, arrays, and nested maps preserved? |

## Chronicle: worktree and repository state reversals

Do not restore an intermediate mechanism just because one commit fixed it.
Follow the whole chain.

| Step and date | Symptom and attempted solution | Root cause, evidence, and status | Settled rule |
|---|---|---|---|
| Existing worktree skipped, 2026-07-23 | `Prepare` returned when `.git` existed. `89530ad` added fetch/checkout/reset to refresh it. | Existence did not prove freshness or completed preparation. Later `5883568` replaced `.git` readiness with a sentinel. **Status:** `superseded` implementation, enduring lesson. | Define a completed-state marker and revalidate remote/base/branch state on reuse. |
| Sentinel outside `.git`, 2026-07-24 | `.archie-prepared` marked successful setup and `.git/info/exclude` kept it untracked. | go-git `Add(All)` does not honor that exclude. `9c5f6a0` moved the sentinel to `.git/archie-prepared`. **Status:** `superseded` location. | Put orchestration metadata outside the tracked worktree by construction, not by ignore convention. |
| Askpass rewritten each call, 2026-07-24 | The “writes once” helper rewrote a token-emitting shell script for every git command. `7f796b3` added `sync.Once`. | The comment was not an invariant and no test checked mtime. `9c5f6a0` later removed shell git and askpass entirely. **Status:** `superseded`. | Prefer in-process credentials. If a secret helper is unavoidable, test lifecycle, permissions, exposure, and cleanup. |
| Bare object cache, 2026-07-24 | `4c1cfb1` added `--reference-if-able --dissociate` plus TTL to accelerate clones while keeping tasks independent. | go-git has shared alternates but no equivalent of `--dissociate`; expiring the cache would corrupt active worktrees. `9c5f6a0` intentionally dropped the Git cache and retained only persistent container volumes. **Status:** `superseded`, not proven “reverted.” | Never accept clone acceleration that makes task correctness depend on an expiring cache. Independence beats reuse. |
| Shell git replaced by go-git, 2026-07-28 | `9c5f6a0` removed token-bearing child environments and no-context shell calls. Porting exposed host GPG inheritance, sentinel tracking, and merge-base diff semantics. | The port stopped at a compiling sibling package: `0a813fb` then found worktree RPC handlers using `context.Background`, making in-process clones unbounded. **Status:** `current` go-git design with settled follow-up. | Audit every adapter and cancellation boundary after replacing a killable subprocess with in-process I/O. Rebuild tests against the new implementation rather than letting old binaries skip them. |

## Chronicle: cleanup, artifacts, and contradictory policy

| Incident and date | Symptom | Attempted solution and root cause | Evidence and status | Settled rule |
|---|---|---|---|---|
| Tracked `.gotmp`, 2026-07-27 | Go build-cache binaries, generated `_testmain.go`, and `.git-askpass` appear in commits. | **Attempt:** use repo-local `.gotmp` to work around tmpfs quota, then broad `git add -A`. **Cause:** the workaround path was not ignored before use. | `da61e4e`/`f715db2` introduced artifacts; `4cb0577` removes them and adds `.gotmp/` to `.gitignore`. Live tracked count is zero. **Status:** `settled`. | Put caches outside the repo or ignore them before the first command. Scan tracked files before accepting a broad add. |
| Tracked `docs/node_modules`, 2026-07-28 | Hundreds of pnpm symlink entries become repository content. | **Attempt:** broad `308c199` cleanup/documentation commit. **Cause:** `docs/node_modules` was neither ignored nor excluded from the commit. | `git ls-files 'docs/node_modules/**'` reports 237 entries at `HEAD`; `.gitignore` has no `node_modules` rule. **Status:** `current/open`. | Track lockfiles, never installed dependency trees. Add the ignore before installation and verify tracked counts are zero. |
| `308c199` cleanup regression, 2026-07-28 | `TestRunWrapsExternalCommand` returns `"\n"` instead of `"wrapped\n"`; forge compatibility is removed again; dependency artifacts are added. | **Attempt:** a 311-file cleanup/source-reference purge changed `exec.Command("echo", "wrapped")` to `exec.CommandContext(ctx, "sh", "-c", "echo", "wrapped")`, where `"wrapped"` becomes shell `$0`, and removed `Forge.TokenEnv`. **Cause:** semantic edits were classified and reviewed as cleanup. | `git show 308c199`; focused skillscript test fails at current `HEAD`; compare `HEAD:internal/config/config.go` with the dirty worktree; tracked node_modules count above. **Status:** `current/open`; do not assume dirty repairs are accepted. | No commit is “cleanup” for gating purposes. Classify each changed behavior, inspect every non-test semantic diff, run the full relevant suite, and scan artifact/compatibility deltas. |
| `diff_cap_lines = 0` contradiction, introduced at project start and exposed 2026-07-23 | Operators set zero expecting unlimited, but decoded config becomes 400. | **Attempt:** `5d60e75` made `StageDiffCap` treat `<=0` as disabled and later Docker config used zero. **Cause:** `finalize` has always converted zero to the default 400, so the same sentinel means both “unset” and “unlimited.” | `internal/config/config.go` defaults zero to 400; `internal/workflow/steps.go` treats zero as unlimited; `config.example.toml` advertises 400. **Status:** `current/open` contradiction. | Never overload a zero value with both defaulting and explicit policy. Use presence-aware decoding or a distinct explicit value, then test config-to-runtime behavior end to end. |

## Keep unmerged work as dated candidates

As of **2026-07-28**, these remote branches are not merged into the checked-out
`HEAD`. Their subjects and diffs are evidence of proposed fixes, not proof that
the live defect or exact patch remains valid.

| Candidate branch | Claimed concern | Required reconciliation |
|---|---|---|
| `origin/fix/43-loaddir-no-timeout-around-yaegi-eval-can-hang-daemon-startup` | Bound Yaegi evaluation during `LoadDir` | Compare current `internal/yaegiutil`, plugin startup, cancellation, and tests before adopting. |
| `origin/fix/45-skillscriptrun-ignores-context-cannot-be-canceledtimed-out` | Make skill scripts cancellable | Reconcile with current `skillscript.Run` API and the `308c199` failing test. |
| `origin/fix/48-transition-ignores-from-status-guard-allowing-lost-updates` | Enforce expected source state atomically | Compare both SQLite and NellDB transition/audit semantics; do not patch only one backend. |
| `origin/fix/52-handlesse-silently-swallows-eventssince-backlog-fetch-errors` | Surface SSE backlog query errors | Recheck current web handler composition and observable HTTP behavior. |
| `origin/feat/51-no-test-coverage-for-stagestats-query` | Add `StageStats` query coverage | Treat as test-only candidate; verify query/schema drift. |
| `origin/feat/75-add-mcp-client-capabilities-to-archied-and-archie-agent` | Wire MCP through both execution modes | Live `HEAD` already contains later MCP/provider work; compare behavior, never cherry-pick the branch wholesale. |

Verify candidate state:

```sh
git branch -r --no-merged HEAD
git log -1 --date=iso-strict --format='%H %ad %s' <branch>
git diff --stat HEAD...<branch>
git merge-base --is-ancestor <branch> HEAD
```

If equivalent behavior exists on `HEAD`, mark the branch `superseded`, not
merged. If no current reproduction exists, leave it `candidate`.

## Produce the archaeology handoff

Return:

1. observation commit and dirty paths;
2. exact symptom and current call path;
3. the closest incident packet with all six fields;
4. differences between the historical and current assemblies;
5. status using the taxonomy;
6. forbidden wrong paths derived from the settled rule;
7. one focused reproduction command;
8. the sibling skill that owns the next step.

Never end with “probably the same bug.” State what is proven, what is inferred,
and what remains unknown.

## Provenance and maintenance

Re-verify the chronology: `git log --all --date=iso-strict --format='%H %ad %s' -- cmd internal config.docker.toml docker-compose.yml go.mod .gitignore`

Re-verify a transition: `git show --no-ext-diff --format=fuller --stat <commit> && git merge-base --is-ancestor <commit> HEAD`

Re-verify live incident anchors: `rg -n 'selfNetwork|TokenEnv|FullStream|readMessage|BuildNoChanges|maybeRetryParked|DiffCapLines|prefetchTimeout|nolint:nilerr' cmd internal`

Re-verify dependency locality: `rg -n '^replace|/work/apps|/work/projects' go.mod tools/go.mod`

Re-verify tracked artifact counts: `git ls-files '.gotmp/**' 'docs/node_modules/**'`

Re-verify branch-only candidates: `git branch -r --no-merged HEAD`

Re-verify the current cleanup regression: `env GOCACHE=/tmp/archie-archaeology-gocache GOTMPDIR=/tmp go test ./internal/skillscript -run '^TestRunWrapsExternalCommand$' -count=1 -v`
