# archie-core Workflows & Skills  --  Product Requirements Document

**Author:** Archie
**Date:** 2026-07-18
**Status:** Draft

---

## Overview

This PRD defines a workflow and skills system for archie-core. Workflow files declare gates, tool availability, execution engines, and skill references. Skills are reusable playbook documents the agent loads on demand. Together they enable any project to define how agents work within their sandbox and select an available execution engine.

---

## 1. Workflow Files (`archie.yaml`)

### 1.1 Discovery

A project declares its workflow in `archie.yaml` at the project root. archie-core discovers it the same way Task (Taskfile) and devcontainers do:

```
.archie.yaml              (root of repo  --  highest precedence)
archie.yaml               (repo root)
.archie/archie.yaml       (subdirectory, one level deep)
```

If no file is found, archie-core uses built-in defaults (read, write, bash).

### 1.2 Schema

```yaml
# archie.yaml  --  complete schema
version: "1"

# Project identity
project:
  name: my-go-service
  language: go

# Available skills (directories or individual files)
skills:
  paths:
    - .archie/skills/          # project-local skills
    - ~/.archie/skills/        # per-user skills
    - github.com/org/shared-skills@v1  # remote skill repo
  skills:
    - go-testing               # named skill from any path
    - security-audit

# Execution engines in preference order
engines:
  - type: claude                # Claude Code CLI
    model: sonnet
  - type: codex                 # OpenAI Codex
  - type: pi                    # Pi agent harness
  - type: raw                   # Direct LLM without agent wrapper

# Sandbox definition (what the Docker container provides)
sandbox:
  image: ghcr.io/samcharles93/archie-sandbox:latest
  tools:
    - go
    - golangci-lint
    - gofumpt
    - task
    - gh
    - docker
  mounts:
    - source: /var/run/docker.sock
      target: /var/run/docker.sock
  env:
    GOFLAGS: "-mod=readonly"
    CGO_ENABLED: "0"

# Gates  --  ordered checkpoints that must pass
gates:
  test:
    run: go test -race -count=1 ./...
    timeout: 300
    description: "All tests must pass"
  lint:
    run: golangci-lint run ./...
    timeout: 120
    description: "Zero lint issues"
  format:
    run: gofumpt -w . && git diff --exit-code
    description: "No formatting drift"
  vet:
    run: go vet ./...
    description: "No vet warnings"
  deadcode:
    run: task deadcode
    optional: true                # non-blocking gate
    description: "Dead code check (advisory)"

# Quality pipeline  --  ordered sequence of gate groups
quality:
  pre_commit:
    - test
    - lint
    - format
    - vet
  pre_push:
    - deadcode

# Commit conventions
commit:
  format: conventional             # conventional commits
  signoff: false
  template: "feat({scope}): {description}"

# PR conventions
pr:
  template: .github/PULL_REQUEST_TEMPLATE.md
  checks:
    - test
    - lint

# Project-specific commands (what `archie run <name>` executes)
commands:
  build:
    run: go build -o bin/app ./cmd/server
    description: "Build the binary"
  serve:
    run: go run ./cmd/server
    description: "Start dev server"
  test:
    run: go test ./...
    description: "Run all tests"
  lint:
    run: golangci-lint run ./...
    description: "Run linter"
  all:
    run:
      - gofumpt -w .
      - go fix ./...
      - golangci-lint run ./...
      - go test -race ./...
      - task deadcode
    description: "Full quality pipeline"
```

### 1.3 Design decisions (with rationale)

| Decision | Rationale |
|---|---|
| YAML over TOML/JSON | Already dominant in CI (GitHub Actions, GitLab CI, Taskfile). Human-readable with comments. |
| Single file over multi-file | Task discovery (like `Taskfile.yml`), devcontainer conventions. Avoids directory sprawl. |
| Gates as named checkpoints | GitHub Actions job model. Gates can be composed into quality pipelines, selected individually, or skipped with `--skip`. |
| Engines as ordered preference list | Supports deterministic fallback between available engines. Each engine declares its own tool surface. |
| Skills as paths + references | Matches devcontainer features (paths resolve to directories) and skills.sh (namespaced references). |
| Commands section for direct invocation | Arnie can `archie run test` without an agent turn. Thin wrapper over the sandbox. |
| Quality pipeline groups gates | Pre-commit vs pre-push vs pre-merge distinction. Some gates are required, some advisory. |

---

## 2. Skills

### 2.1 Format

Skills follow the [agentskills.io](https://agentskills.io/specification) specification:

```
skill-name/
├── SKILL.md          # Required: YAML frontmatter + Markdown instructions
├── scripts/          # Optional: executable helpers
├── references/       # Optional: longer docs, checklists
└── assets/           # Optional: templates, configs
```

**SKILL.md frontmatter:**

```yaml
---
name: go-testing
description: >
  Run Go tests with race detection, coverage, and benchmark analysis.
  Use when asked to test Go code, run the test suite, or check coverage.
version: 1.0.0
metadata:
  archie:
    gates: [test]                    # gates this skill requires
    tools: [go, golangci-lint]       # sandbox tools it needs
    engine: any                      # engine preference
compatibility: Requires Go 1.22+
---
```

### 2.2 Progressive disclosure

Following the skills.sh and Claude Code patterns:

1. **Advertise** (~100 tokens): `name` + `description` loaded at session start
2. **Load** (<5000 tokens recommended): Full SKILL.md body when skill activates
3. **Resources** (as needed): Scripts, references, assets loaded on demand

### 2.3 archie-specific metadata

Skills can carry archie-specific frontmatter under `metadata.archie`:

```yaml
metadata:
  archie:
    gates: [test, lint]              # must-pass gates before skill runs
    tools: [go, docker]              # sandbox tools required
    engine: any                      # preferred engine
    model: gpt-4o                    # preferred model
    budget_usd: 2.00                 # max spend ceiling
    timeout: 600                     # max seconds
    worktree: true                   # create isolated worktree
```

### 2.4 Skill sources (inspired by devcontainer features + Dagger modules)

```
skills:
  paths:
    - .archie/skills/                              # project-local (committed)
    - ~/.archie/skills/                            # per-user (personal)
  skills:
    - go-testing                                   # by name from any path
  remotes:
    - github.com/samcharles93/archie-skills@v1     # tagged git repo
    - https://agentskills.io/registry              # community registry
```

---

## 3. Execution Engine Model

### 3.1 Engine abstraction

Each engine provides the same interface. The workflow file declares preference order; archie-core resolves the first available engine at runtime.

```
Engine interface:
  - Run(prompt, tools[], budget, timeout) -> Result
  - Tools() -> []ToolSchema
  - Capabilities() -> {streaming, reasoning, vision, agentic}
```

### 3.2 Supported engines

| Engine | Type | What it provides |
|---|---|---|
| `claude` | Claude Code CLI | Full agent loop, tool use, worktrees, subagents |
| `codex` | OpenAI Codex CLI | Agentic coding with sandbox |
| `pi` | Pi agent harness | Minimalist coding agent, stdio protocol |
| `raw` | Direct LLM call | Single-turn prompt, no agent loop. For simple scripts. |

### 3.3 Engine selection logic

```yaml
engines:
  - type: claude
    model: sonnet
    budget_usd: 5.00
  - type: raw
    model: gpt-4o-mini
```

arcie-core tries engines in order and selects the first available,
authenticated engine. Each engine declares its budget ceiling.

---

## 4. Additional Features

### 4.1 Worktree isolation (`worktree: true`)

Inspired by Claude Code's `--worktree` flag. When a skill or command sets `worktree: true`, archie-core:

1. Creates an isolated git worktree at `.archie/worktrees/<skill-name>-<timestamp>/`
2. Runs all gates and implementation in that worktree
3. Cleans up on success, preserves on failure for inspection

This prevents concurrent agent sessions from conflicting and enables parallel agent execution.

### 4.2 Skill chaining and composition

Skills can declare dependencies, forming a DAG:

```yaml
---
name: full-ci
description: Complete CI pipeline including security audit
metadata:
  archie:
    requires: [go-testing, security-audit]
    run_sequential: true
---
```

arcie-core resolves the dependency graph and runs skills in order (parallel where possible, respecting `run_sequential`). This is conceptually similar to GitHub Actions `needs` and Taskfile `deps`.

### 4.3 Gate overrides per skill

A skill can override project-level gates:

```yaml
metadata:
  archie:
    gates:
      test:
        run: go test -short ./...      # override: faster test for this skill
      lint: skip                        # skip lint for this skill
```

Three override modes: `override` (replace command), `skip` (omit gate), `add` (add new gate requirement).

### 4.4 Skill templates (scaffolding)

Skills can carry an `assets/` directory with templates. When archie-core initializes a new project, it can apply skill templates:

```
skill-name/
├── SKILL.md
└── assets/
    ├── .archie.yaml.tmpl       # Template workflow file
    ├── Taskfile.yml.tmpl       # Template task runner
    └── .golangci.yml.tmpl      # Template linter config
```

`archie init --skill go-service` scaffolds a new project with the go-service skill's templates applied.

### 4.5 Blueprint export (external cron integration)

Any archie-core workflow can be exported as an automation blueprint:

```yaml
metadata:
  archie:
    blueprint:
      schedule: "every 6h"
      deliver: telegram
      name: "CI monitor"
```

The export produces a `SKILL.md` with scheduler metadata. The blueprint's prompt is the archie workflow's quality pipeline.

---

## 5. Comparison to Existing Systems

| Feature | archie-core | GitHub Actions | Dagger | Taskfile | devcontainer | External Skills |
|---|---|---|---|---|---|---|
| Language | YAML | YAML | Go/Python/TS | YAML | JSON | Markdown |
| Gates | Named + composable | Steps in jobs | Functions | Tasks | Lifecycle scripts | N/A |
| Engine abstraction | First-class | GitHub only | Dagger Engine | Shell only | None | Provider |
| Skills | agentskills.io spec | Composite actions | Dagger Modules | Includes | Features | SKILL.md |
| Worktree isolation | Built-in | No | Containers | No | Container | No |
| Blueprint export | To external cron | No | No | No | No | Native |
| Multi-engine fallback | Yes (ordered list) | No | No | No | No | Provider config |
| Progressive disclosure | Yes (skill spec) | No | No | No | Feature metadata | Yes |

---

## 6. Implementation Phases

### Phase 1: Core workflow file
- `archie.yaml` parser and schema validation
- Gate runner (test, lint, format, vet)
- `archie run <command>` CLI
- Sandbox Docker integration

### Phase 2: Skills
- SKILL.md discovery and progressive disclosure
- Skill metadata with gate/tool/engine requirements
- `archie skills list/install/search`

### Phase 3: Engine abstraction
- Engine interface definition
- Claude Code adapter
- Raw LLM adapter
- Engine fallback chain

### Phase 4: Advanced features
- Worktree isolation
- Skill chaining (dependency DAG)
- Gate overrides
- Blueprint export

### Phase 5: Ecosystem
- Public skill registry
- `archie init --skill <name>` scaffolding
- Community skill contributions

---

## 7. Open Questions

1. **Skill registry hosting:** Self-hosted on git.catlow.cloud, or use agentskills.io?
2. **External integration:** Should archie-core be exposed as a tool, a skill, or standalone?
3. **Budget enforcement:** Per-skill budget vs. per-workflow budget vs. monthly cap?
4. **Sandbox image building:** Pre-built images vs. Dockerfile in each skill?
