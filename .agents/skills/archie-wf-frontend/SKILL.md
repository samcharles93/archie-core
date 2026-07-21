---
name: archie-wf-frontend
description: >
  Runs frontend quality checks: stylelint for CSS, htmlhint for HTML.
  Gate stage runs these after Go vet/build/test. Use in any project
  with web assets to catch syntax errors, broken selectors, and
  structural issues before they reach a PR.
version: 1.1.0
metadata:
  archie:
    workflow: frontend
    tools: [stylelint, htmlhint]
    engine: any

# Frontend Quality Gate

## When this runs

This skill adds frontend linting to the gate stage. After Go checks pass,
the gate runs:

1. `stylelint "**/*.css"` — catches syntax errors, invalid properties,
   duplicate selectors, calc() misuse, missing units
2. `htmlhint "**/*.html"` — catches unclosed tags, mismatched pairs,
   duplicate IDs, inline style abuse, missing viewport meta

Both must exit 0. If either fails, the stage parks with the lint output.

## How to use in your project

1. Copy `.agents/skills/archie-wf-frontend/` into your repo
2. Add to your daemon config gate:
   ```toml
   gate = [
     ["go", "vet", "./..."],
     ["go", "test", "./..."],
     ["stylelint", "**/*.css"],
     ["htmlhint", "**/*.html"],
   ]
   ```
3. Or declare `workflow: frontend` in the skill frontmatter — archie-core
   loads the skill body and the agent knows to run these during build

