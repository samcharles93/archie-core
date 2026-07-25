# archie-core Skills System  --  PRD

**Author:** Archie (Hermes agent)
**Date:** 2026-07-18
**Status:** Draft

---

## What archie-core is

arcie-core is an LLM coding agent that responds to issues, queries, and commands on connected interfaces. The current MVP runs on GitHub: it receives issue assignments, analyses scope and requirements, implements changes in isolated worktrees, runs quality gates, and opens pull requests.

The skills system extends this with reusable playbooks that tell archie-core **how** to work on any given project.

---

## 1. The workflow file (`archie.yaml`)

Every project can ship an `archie.yaml` that defines how archie-core should behave when working on that codebase. Without one, archie-core uses sensible defaults (read files, run bash, open PRs). With one, it knows the exact gates to pass, tools available, commit format, and which skills are relevant.

### 1.1 Discovery

```
.archie.yaml              (root of repo  --  highest precedence)
archie.yaml               (repo root)
.archie/archie.yaml       (subdirectory, one level deep)
```

### 1.2 Schema

```yaml
# archie.yaml
version: "1"

# Project identity
project:
  name: my-go-service
  language: go
  repo: github.com/sam/my-go-service

# How archie-core connects to this project
connection:
  interfaces:
    - github            # respond to assigned issues
    - linear            # respond to labelled tickets
    - webhook           # respond to HTTP POSTs
  github:
    repo: sam/my-go-service
    issue_labels:
      ready: "archie:ready"      # issues with this label get claimed
      claimed: "archie:claimed"  # archie sets this when working
      done: "archie:done"        # archie sets this after PR
    pr:
      base: main
      draft: false

# Skills  --  playbooks archie-core loads to know how to work
skills:
  paths:
    - .archie/skills/              # project-local skills
    - ~/.archie/skills/            # per-user skills
  remotes:
    - github.com/sam/archie-skills@v1   # shared skill library

# Sandbox  --  what's available in the container
sandbox:
  image: ghcr.io/sam/archie-sandbox:latest
  tools:
    - go
    - golangci-lint
    - gofumpt
    - task
    - gh
    - docker
  env:
    GOFLAGS: "-mod=readonly"

# Gates  --  archie-core must pass these before committing
gates:
  test:
    run: go test -race -count=1 ./...
    timeout: 300
  lint:
    run: golangci-lint run ./...
    timeout: 120
  format:
    run: gofumpt -w . && git diff --exit-code
  deadcode:
    run: task deadcode
    optional: true

# Quality pipeline  --  which gates run when
quality:
  pre_commit: [test, lint, format]
  pre_push: [deadcode]

# Commit conventions
commit:
  format: conventional
  signoff: false

# Branch conventions
branch:
  format: "{type}/cat-{number}-{kebab-description}"
  types:
    feat: "Feature work"
    fix: "Bug fixes"
    test: "Test additions"
    chore: "Maintenance"
    refactor: "Code restructuring"
    docs: "Documentation"
```

---

## 2. Skills (playbooks)

Skills are Markdown documents that tell archie-core **how** to perform specific tasks. They follow the [agentskills.io](https://agentskills.io/specification) format, making them portable across LLM coding agents (Claude Code, Codex, Tau, Pi, archie-core).

### 2.1 Format

```
go-testing/
├── SKILL.md              # Required: frontmatter + instructions
├── references/           # Optional: supplementary docs
│   └── coverage.md
├── scripts/              # Optional: executable helpers
│   └── bench.sh
└── assets/               # Optional: templates
    └── test.vscode.code-snippets
```

### 2.2 SKILL.md

```markdown
---
name: go-quality-gate
description: >
  Run the full Go quality pipeline for Tau projects:
  gofumpt, go fix, golangci-lint, go test -race, and task deadcode.
  Use before any commit in a Go project.
version: 1.0.0
metadata:
  archie:
    # Which gates this skill covers
    gates: [test, lint, format, deadcode]
    # Required sandbox tools
    tools: [go, golangci-lint, gofumpt, task]
    # Preferred engine (if multiple available)
    engine: any
    # Max spend for this skill
    budget_usd: 2.00
---

# Go Quality Gate

## When to use
Before committing any Go change. After implementing a feature or fix.

## Procedure
Run in this exact order. If any step fails, fix the issue before continuing.

1. `gofumpt -w .`
2. `go fix ./...`
3. `golangci-lint run ./...`
4. `go test -race -count=1 ./...`
5. `task deadcode`

## Verification
All five steps must exit 0. If `task deadcode` fails on unrelated packages,
scope it to only changed packages: `golangci-lint run --tests=false --enable-only=unused,staticcheck ./changed/pkg/...`

## Common failures
- `golangci-lint`: unused function  --  remove it or add `//nolint:unused` comment
- `go test -race`: data race  --  protect with mutex or channel
- `task deadcode`: pre-existing dead code in unrelated package  --  scope to changed packages
```

### 2.3 Skill metadata (arcie-specific)

Skills carry archie-specific metadata under `metadata.archie`:

| Field | Purpose |
|---|---|
| `gates` | Which gates from `archie.yaml` this skill covers |
| `tools` | Sandbox tools this skill needs |
| `engine` | Preferred execution engine (`claude`, `tau`, `codex`, `pi`, `raw`, `any`) |
| `model` | Preferred model for this skill |
| `budget_usd` | Max spend ceiling |
| `timeout` | Max seconds |
| `worktree` | Whether to create isolated worktree |

### 2.4 Progressive disclosure

Following the skills.sh model to keep context lean:

1. **Advertise** (~100 tokens per skill): `name` + `description` loaded at startup
2. **Activate** (<5000 tokens): Full SKILL.md body loaded when skill is relevant
3. **Resources** (as needed): References, scripts, assets loaded on demand

---

## 3. How archie-core processes an issue

This is the core loop. When an issue lands with the configured label:

### 3.1 Analyse

1. Read issue title, body, labels, comments
2. Load relevant skills from the project's `archie.yaml`
3. Determine scope: which packages, files, and gates are in play
4. Determine branch type (`feat/`, `fix/`, `test/`, etc.) from issue labels or conventional commit prefix

### 3.2 Claim

1. Set issue label to `archie:claimed`
2. Comment: "Archie claimed this. Branch: `<branch>`"
3. Create isolated worktree from `main`

### 3.3 Implement

1. Load full skill bodies for each relevant skill
2. Read project source and tests in the affected area
3. Implement changes, writing tests first where applicable
4. Run targeted tests iteratively

### 3.4 Gate

1. Run all gates from the quality pipeline in order
2. Fix any failures, re-running from the failing gate
3. Audit diff for secrets, unrelated changes, attribution

### 3.5 Deliver

1. Commit with conventional format: `feat(scope): description`
2. Push branch
3. Open PR to `main` with:
   - Summary of changes
   - Acceptance criteria addressed
   - Gate results
   - Link back to source issue/Linear ticket
4. Set issue label to `archie:done`
5. Comment with PR URL

### 3.6 On failure

If any gate fails irrecoverably:
1. Leave issue as `archie:claimed`
2. Comment with exact blocker, failed command, and worktree path
3. Preserve worktree for inspection
4. Do not open a broken PR

---

## 4. Engine abstraction

arcie-core can delegate implementation to different LLM engines. The workflow file declares preference order; archie-core resolves the first available engine at runtime.

```yaml
engines:
  - type: claude            # Claude Code CLI  --  full agent loop
    model: sonnet
    budget_usd: 5.00
  - type: tau               # Tau  --  Sam's native Go agent
    model: gpt-4o
    budget_usd: 2.00
  - type: codex             # OpenAI Codex CLI
    model: gpt-5.1-codex-max
  - type: raw               # Direct LLM  --  no agent loop, single prompt
    model: gpt-4o-mini
```

Each engine provides the same interface:

```
Engine:
  - Run(prompt, tools, budget, timeout) -> Result
  - Skills() -> []SkillName
  - Capabilities() -> {streaming, reasoning, vision, agentic}
```

For skills that need a specific engine, the skill metadata overrides:

```yaml
metadata:
  archie:
    engine: claude           # this skill only works with Claude
```

---

## 5. Skills in practice

### 5.1 Project-specific skill

```yaml
# .archie/skills/tau-quality-gate/SKILL.md
---
name: tau-quality-gate
description: >
  Full Tau quality pipeline: gofumpt, go fix, golangci-lint,
  go test -race, task deadcode. Use for all Tau repo changes.
metadata:
  archie:
    gates: [test, lint, format, deadcode]
    tools: [go, golangci-lint, gofumpt, task]
---
```

### 5.2 Shared skill library

```yaml
# archie.yaml
skills:
  remotes:
    - github.com/sam/archie-skills@v1
```

Skills from the shared library are available alongside project-local skills. The agent loads them on demand when an issue references relevant concepts.

### 5.3 Skill that wraps an external tool

```yaml
---
name: trivy-scan
description: >
  Scan container images and filesystems for vulnerabilities using Trivy.
  Use when asked about security scanning, CVEs, or container hardening.
metadata:
  archie:
    tools: [docker, trivy]
    engine: any
---

## Procedure
1. `trivy image --severity HIGH,CRITICAL <image>`
2. If findings exist, report each CVE with package, version, and fixed version
3. If no findings, report clean
```

---

## 6. Comparison: archie-core vs existing systems

| | archie-core | Claude Code | Codex CLI | GitHub Actions | Hermes Agent |
|---|---|---|---|---|---|
| **How it receives work** | GitHub issue assignment, Linear label, webhook | Chat, slash commands | Chat, slash commands | YAML triggers (push, PR, schedule) | Chat, cron, webhooks |
| **What it does** | Analyse → implement → gate → PR | Analyse → implement → gate → PR | Analyse → implement → gate → PR | Run arbitrary shell commands | Analyse → implement → gate → PR |
| **Skills** | SKILL.md (agentskills.io) | SKILL.md (native) | SKILL.md (agentskills.io) | Composite actions | SKILL.md (native) |
| **Gates** | Named checkpoints in archie.yaml | Ad-hoc (CLAUDE.md + lint) | Ad-hoc | Steps in jobs | Skills with gates |
| **Engine pluggable** | Yes (claude, tau, codex, pi, raw) | No (fixed to Claude) | No (fixed to Codex) | No (fixed to GitHub) | Yes (provider config) |
| **Worktree isolation** | Yes | Yes (`--worktree`) | No | No (ephemeral runner) | No |
| **Blueprint/cron** | Export to Hermes | No | No | Schedule trigger | Native |
| **Where it runs** | Docker sandbox | Host | Host or sandbox | GitHub runner | Host, Docker, Modal, SSH |

---

## 7. Implementation phases

### Phase 1: Workflow file and gates (current MVP)
- `archie.yaml` parser
- Gate runner executing shell commands and checking exit codes
- Skill discovery from project and user paths
- GitHub interface: claim issues, create worktrees, open PRs

### Phase 2: Skills
- SKILL.md progressive disclosure (advertise → activate → resources)
- archie-specific metadata parsing
- Skill chaining: skills that depend on other skills
- `archie skills list/install` CLI

### Phase 3: Engine abstraction
- Engine interface definition
- Claude Code adapter
- Raw LLM adapter
- Engine fallback chain
- Per-skill engine preferences

### Phase 4: Advanced features
- Gate overrides per skill
- Skill templates for project scaffolding (`archie init --skill go-service`)
- Remote skill libraries (git repos as skill sources)
- Blueprint export to Hermes cron

### Phase 5: Additional interfaces
- Linear webhook integration
- Generic webhook interface for arbitrary sources
- CLI command interface (`archie run <command>`)

---

## 8. Open questions

1. **Claim protocol:** Single-claim (arcie takes one issue at a time) or queue-based (claims N issues, works through them sequentially)?
2. **Concurrent worktrees:** Allow multiple archie-core instances working on different issues simultaneously?
3. **PR merging:** Should archie-core ever auto-merge, or always require human review?
4. **Skill registry:** Self-host on git.catlow.cloud, use agentskills.io, or both?
5. **Engine budget enforcement:** Per-issue, per-skill, or monthly cap?
6. **Sandbox building:** Pre-built Docker images vs. project-defined Dockerfiles vs. Nix shells?
