---
name: archie-wf-frontend
description: >
  Validates HTML, CSS, and JS in the worktree before commit. Runs as a
  gate stage: parses CSS for syntax errors, checks HTML structure, and
  verifies responsive requirements. Use in any project with web assets.
version: 1.0.0
metadata:
  archie:
    workflow: frontend
    tools: [go]
    engine: any
    plugins:
      - plugins/css-gate.go
      - plugins/html-gate.go
---

# Frontend Quality Gate

## When this runs

This skill registers a `frontend` workflow. Repos that include web assets
(HTML, CSS, JS) can opt into it by labelling issues `frontend` or declaring
`workflow: frontend` in their SKILL.md frontmatter.

The bundled plugins run during the gate stage:

- `css-gate.go` — parses all .css files and `<style>` blocks for syntax
  errors, missing units, invalid calc() expressions, and common responsive
  anti-patterns
- `html-gate.go` — checks HTML files for orphaned tags, missing closing
  elements, and structural issues

Both plugins return `Finding` structs. Error-level findings block the
commit. Warn-level findings are logged.

## How to use in your project

1. Copy `.agents/skills/archie-wf-frontend/` into your repo
2. Ensure `workflow: frontend` is declared in the skill frontmatter
3. Label UI/design issues `frontend` — archie-core routes them here
4. The gate runs after Go vet/build/test (unchanged — this skill adds
   to the existing gate, it doesn't replace it)

## Verification

After a frontend-labelled issue is implemented, archie-core runs:
```
go vet ./...        # existing
go build ./...      # existing
go test ./...       # existing
css-gate            # new — CSS validation
html-gate           # new — HTML validation
```
