# skills

Here is the complete analysis.

---

## Complete Report: SKILL.md Files in archie-core

### 1. Tally of files

There are exactly **5 SKILL.md files** in the repository, all under `/work/apps/archie-core/.archie/skills/`. There are no others anywhere in the repo.

---

### 2. Supporting directories

There are **no `plugins/` or `scripts/` subdirectories** inside any skill directory. The `scripts/` feature described in the agentskills.io spec and the Yaoi interpreter (`internal/skillscript/yaegi.go`) exist in the Go code but have no content in the actual skill directories. The `run_go_script` tool in `internal/agentexec/inprocess.go` (lines 181-207) references scripts at `.archie/skills/<skill>/scripts/*.go`, but no such files exist.

---

### 3. Per-file analysis

---

#### 3.1 tdd-bugfix/SKILL.md

**File:** `/work/apps/archie-core/.archie/skills/tdd-bugfix/SKILL.md`

**Complete YAML frontmatter:**

```yaml
name: tdd-bugfix
description: >
  Fix bugs using test-driven development. Write a failing repro test first, prove
  it captures the bug, then fix the code without touching the tests. archie-core
  routes issues labelled "bug" through this workflow automatically.
version: 1.0.0
metadata:
  archie:
    tools: [go, golangci-lint]
    engine: any
```

Key-value pairs extracted:
- `name`: "tdd-bugfix"
- `description`: Multiline string (about TDD bugfix)
- `version`: "1.0.0"
- `metadata.archie.tools`: [go, golangci-lint]
- `metadata.archie.engine`: "any"

**Body content structure:**

The body has 6 sections:
1. `## When this runs` (1 paragraph: describes that archie-core routes "bug" labelled issues through this workflow; mentions the 5 stages)
2. `## Stage 1: Analyse (read-only planner)` (prose: what the planner produces, no-code constraint)
3. `## Stage 2: Repro Tests (write failing tests only)` (prose with bullet constraints: don't touch non-test files, gate inverted, make failure explicit)
4. `## Stage 3: Capture Proof (deterministic)` (prose: archie-core runs the test command directly)
5. `## Stage 4: Fix (code only)` (prose with bullet constraints: test files write-protected, full gate runs, smallest fix)
6. `## Stage 5: Deliver` (numbered list: commits, push, PR, comment with proof)
7. `## Common pitfalls` with 3 sub-pitfalls (each with prose and checklist)

**Mapping to hardcoded Go code:**

This skill documents the exact workflow that is hardcoded in `internal/workflow/tdd.go` lines 18-149. The 5 stages correspond directly:
- "analyse" -> `AgentStage{Name: "analyse", ...}` (line 27)
- "repro-tests" -> `AgentStage{Name: "repro-tests", ...}` (line 48)
- "capture-proof" -> `Stage{Name: "capture-proof", Run: ...}` (line 73)
- "fix" -> `AgentStage{Name: "fix", ...}` (line 92)
- "deliver" -> Stage `open-pr` (line 133) plus the two commit stages (lines 88, 125)

Every constraint in the SKILL.md body (inverted gate, test file write-protection, smallest fix, two-commit history) is reflected as code in `tdd.go` and `agent.go`.

---

#### 3.2 go-quality-gate/SKILL.md

**File:** `/work/apps/archie-core/.archie/skills/go-quality-gate/SKILL.md`

**Complete YAML frontmatter:**

```yaml
name: go-quality-gate
description: >
  Run the full Go quality pipeline: gofumpt, go fix, golangci-lint, go test -race,
  and task deadcode. Use before any commit in a Go project. Triggered automatically
  by archie-core's implement and tdd workflows via the per-repo gate config.
version: 1.0.0
metadata:
  archie:
    tools: [go, golangci-lint, gofumpt, task]
    engine: any
```

Key-value pairs extracted:
- `name`: "go-quality-gate"
- `description`: Multiline string (about Go quality pipeline)
- `version`: "1.0.0"
- `metadata.archie.tools`: [go, golangci-lint, gofumpt, task]
- `metadata.archie.engine`: "any"

**Body content structure:**

5 sections with mixed content:
1. `## When this runs` (prose: gate is enforced after every builder stage; commands come from repo config)
2. `## Standard gate (Tau and similar Go projects)` (TOML examples showing gate commands, plus convention about last command being test runner)
3. `## Diagnosing gate failures` with 4 subsections (each with prose and common fixes):
   - `### gofumpt / go fix`
   - `### golangci-lint` (bullet list of common linters and fixes)
   - `### go test -race` (numbered diagnostic steps)
   - `### task deadcode` (bash example for scoping)
4. `## When the TDD workflow runs` (prose about inverted gate during repro-tests)

**Mapping to hardcoded Go code:**

- The gate mechanism is implemented in `internal/workflow/agent.go` lines 152-161 (`GateFromRepo()`) and `internal/workflow/tdd.go` lines 155-174 (`tddReproGate()`).
- The `tools` list maps conceptually to `config.Repo.Gate` in `internal/config/config.go` lines 37-43 (a `[][]string` of commands).
- The skill documents conventions, not code that loads the SKILL.md.

---

#### 3.3 security-audit/SKILL.md

**File:** `/work/apps/archie-core/.archie/skills/security-audit/SKILL.md`

**Complete YAML frontmatter:**

```yaml
name: security-audit
description: >
  Scan code for security vulnerabilities: hardcoded secrets, injection vectors,
  unsafe deserialization, missing auth checks, and path traversal. Use on PRs
  touching auth, API endpoints, or data handling code.
version: 1.0.0
metadata:
  archie:
    tools: [golangci-lint, trivy, gitleaks]
    engine: any
```

Key-value pairs extracted:
- `name`: "security-audit"
- `description`: Multiline string (about security scanning triggers)
- `version`: "1.0.0"
- `metadata.archie.tools`: [golangci-lint, trivy, gitleaks]
- `metadata.archie.engine`: "any"

**Body content structure:**

4 top-level sections:
1. `## When to use` (bullet list of trigger conditions: PR touches auth, new API endpoints, db queries, dependency updates)
2. `## What to check` with 5 numbered subsections:
   - `### 1. Hardcoded secrets` (bullet list of scan targets + `gitleaks` example)
   - `### 2. Injection vectors` (SQL, Command, Path sub-bullets with specific Go tools)
   - `### 3. Unsafe deserialization` (bullet list of Go-specific patterns)
   - `### 4. Missing auth checks` (bullet list of endpoint/token issues)
   - `### 5. Dependency CVEs` (`trivy` command example + report format)
3. `## Reporting` (numbered list of 5 report fields: Severity, Location, Description, Fix, Prevention)
4. Closing line: "If nothing is found, report clean. Never fabricate findings."

**Mapping to hardcoded Go code:**

- **No hardcoded workflow exists** for security-audit in the Go codebase. This skill is purely informational/documentation.
- It is not registered as a workflow in `cmd/archied/main.go` lines 163-169.
- There is no label routing for security tasks in `internal/workflow/workflow.go` lines 100-141.
- It exists as a skill that could be invoked manually or through future orchestration.

---

#### 3.4 ecosystem-node/SKILL.md

**File:** `/work/apps/archie-core/.archie/skills/ecosystem-node/SKILL.md`

**Complete YAML frontmatter:**

```yaml
name: ecosystem-node
description: >
  Node.js/TypeScript project conventions for archie-core: npm/pnpm, eslint,
  prettier, tsc, vitest. Use when working on Node repositories with archie-core.
version: 1.0.0
metadata:
  archie:
    tools: [node, npm, pnpm, vitest, eslint, prettier]
    engine: any
```

Key-value pairs extracted:
- `name`: "ecosystem-node"
- `description`: Multiline string (about Node/TS project conventions)
- `version`: "1.0.0"
- `metadata.archie.tools`: [node, npm, pnpm, vitest, eslint, prettier]
- `metadata.archie.engine`: "any"

**Body content structure:**

7 sections:
1. `## Preflight` (prose + mention of `node --version`)
2. `## Recommended gate configuration` (TOML example blocks for pnpm and npm)
3. `## Test glob` (prose: default `*.test.ts`, override config examples)
4. `## Package management` (prose + bash: lockfile detection, install command)
5. `## Common tools` (markdown table: Tool, Purpose, Config file — 5 rows)
6. `## Common gate failures` with 3 subsections:
   - `### eslint` (bullet list)
   - `### tsc / typecheck` (bullet list)
   - `### vitest / jest` (bullet list)

**Mapping to hardcoded Go code:**

The content maps directly to `internal/config/ecosystem.go` lines 22-25:
```go
"node": {
    Preflight: [][]string{{"node", "--version"}},
    TestGlob:  "*.test.ts",
},
```
The preflight check and default test glob are hardcoded. Lockfile detection logic is referenced in prose but not implemented in the Go code shown.

---

#### 3.5 ecosystem-python/SKILL.md

**File:** `/work/apps/archie-core/.archie/skills/ecosystem-python/SKILL.md`

**Complete YAML frontmatter:**

```yaml
name: ecosystem-python
description: >
  Python project conventions for archie-core: venv, pytest, ruff, mypy, pip-audit.
  Use when working on Python repositories with archie-core.
version: 1.0.0
metadata:
  archie:
    tools: [python, pytest, ruff, mypy, pip-audit]
    engine: any
```

Key-value pairs extracted:
- `name`: "ecosystem-python"
- `description`: Multiline string (about Python project conventions)
- `version`: "1.0.0"
- `metadata.archie.tools`: [python, pytest, ruff, mypy, pip-audit]
- `metadata.archie.engine`: "any"

**Body content structure:**

7 sections:
1. `## Preflight` (prose + `python --version`)
2. `## Recommended gate configuration` (TOML example)
3. `## Test glob` (prose: default `test_*.py`, override example)
4. `## Package management` (bullet list of assumptions)
5. `## Virtual environment` (bash examples for venv setup)
6. `## Common gate failures` with 3 subsections:
   - `### ruff` (bullet list)
   - `### mypy` (bullet list)
   - `### pytest` (bullet list)

**Mapping to hardcoded Go code:**

Maps directly to `internal/config/ecosystem.go` lines 18-21:
```go
"python": {
    Preflight: [][]string{{"python", "--version"}},
    TestGlob:  "test_*.py",
},
```

---

### 4. The agentskills.io Specification

The specification at `https://agentskills.io/specification` defines these fields:

| Field | Required | Notes |
|---|---|---|
| `name` | Yes | 1-64 chars, lowercase + hyphens, matches directory name |
| `description` | Yes | 1-1024 chars, describes what and when |
| `license` | No | Short license name or reference |
| `compatibility` | No | Environment requirements (product, packages, network) |
| `metadata` | No | Arbitrary key-value map for additional properties |
| `allowed-tools` | No | Space-separated string of pre-approved tools (experimental) |

The spec also defines optional subdirectories: `scripts/`, `references/`, `assets/`.

**Contrast with current archie-core format:**

- `version: 1.0.0` exists in all 5 skills but is **not an agentskills.io field** -- it is custom archie-core metadata inside `metadata.archie`. The spec would expect `metadata.version` or simply omitting version from the spec-level fields.
- `metadata.archie.tools` is a custom archie extension. The spec has `allowed-tools` (string format, experimental) which serves a similar purpose but with different semantics (pre-approval for tool use vs. listing required tools).
- `metadata.archie.engine: any` is custom archie metadata with no spec equivalent.
- None of the current skills use `license`, `compatibility`, or `allowed-tools` from the spec.

---

### 5. Analysis: Which fields would be useful as machine-readable skill configuration

Based on how the Go code currently consumes configuration and how the workflow engine works (`internal/workflow/workflow.go`, `internal/workflow/agent.go`, `internal/config/config.go`), the following would benefit from machine parsing:

**A. `tools` / `allowed-tools` (pre-approval list)**
- Currently hardcoded per skill in `metadata.archie.tools` but not parsed by any Go code.
- Would enable the agent runtime to pre-authorize external tool calls (golangci-lint, trivy, gitleaks, etc.) without prompting, matching the agentskills.io experimental `allowed-tools` concept.
- The TDD workflow's gate mechanism already runs these tools via `config.Repo.Gate` -- a parsed tools list could validate the gate commands against the skill's declared toolset.

**B. `ecosystem` (language/runtime constraint)**
- Currently configured per-repo in TOML as `config.Repo.Ecosystem` (via `internal/config/ecosystem.go`), not per-skill.
- The ecosystem-node and ecosystem-python skills document conventions that overlap with the Go hardcodes. A machine-readable ecosystem field on a skill would let the skill declare its language affinity.
- No skill currently has this as a top-level field.

**C. `engine` constraint (e.g., any, archied v1, ...)**
- Currently `metadata.archie.engine: any` in every skill, not parsed.
- Could gate skill activation to specific engine versions (`archie-core >= 0.5`, `claude-code >= 1.0`).

**D. `budget` (max steps, max tokens, wall clock)**
- Currently hardcoded per workflow in Go: `internal/workflow/agent.go` lines 60-67 reads from `config.Budgets` with per-AgentStage overrides (e.g., MaxSteps=15 for planner, 20 for feasiability PRD stage).
- A machine-readable budget field could let the skill declare its expected resource needs, overriding global defaults when appropriate.

**E. `plugins` (extension scripts)**
- Not present in any current skill. The `internal/skillscript/yaegi.go` infrastructure supports running `.go` scripts from a skill's `scripts/` directory, but no skill has one.
- Could declare scripts that the agent runtime should load and expose as tools.

**F. Stage/workflow structure**
- The body text of tdd-bugfix describes a 5-stage workflow with named stages, but this is natural language only.
- The Go workflows are hardcoded in `internal/workflow/tdd.go`, `implement.go`, `feasibility.go`, and `steps.go`.
- A machine-readable stages list in the frontmatter or a companion YAML file would allow the workflow engine to load workflows from skills without recompilation. Currently, new workflows require Go code changes in `cmd/archied/main.go` lines 163-169.

**G. Gate configuration**
- The quality gate is documented in `go-quality-gate/SKILL.md` as TOML examples but configured per-repo in the daemon's TOML config. A skill-declared gate skeleton would let the skill say "I need these commands to verify correctness."

---

### 6. Summary of the gap

The current system has **no Go code that reads SKILL.md files programmatically**. The files exist purely as human-readable documentation. All workflow logic is hardcoded in Go:

- `internal/workflow/implement.go` -- plan + build stages
- `internal/workflow/tdd.go` -- analyse + repro-tests + capture-proof + fix + deliver
- `internal/workflow/feasibility.go` -- assess + prd + deliver
- `internal/workflow/steps.go` -- shared steps (prepare, commit, push, gate, diff cap, PR)
- `internal/workflow/agent.go` -- AgentStage adapter that turns workflow config into `agentexec.Request`

The only code that touches skill directories is `/work/apps/archie-core/internal/skillscript/yaegi.go` (interprets `scripts/*.go` files) and the `scriptToolSet` in `/work/apps/archie-core/internal/agentexec/inprocess.go` lines 181-207 (exposes the `run_go_script` tool to agents). Both of these are unused since no skill has any `scripts/` content.

To make skills machine-parseable, the frontmatter would need to adopt the agentskills.io standard fields plus archie-specific extensions (tools list as per spec's `allowed-tools` or a custom `metadata.archie` block, plus possibly a declarative stage/workflow scheme in the frontmatter or a companion YAML file).