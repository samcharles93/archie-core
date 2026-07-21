---
name: ecosystem-node
description: >
  Node.js/TypeScript project conventions for archie-core: npm/pnpm, eslint,
  prettier, tsc, vitest. Use when working on Node repositories with archie-core.
version: 1.0.0
metadata:
  archie:
    tools: [node, npm, pnpm, vitest, eslint, prettier]
    engine: any
---
# Node.js / TypeScript Ecosystem Conventions

## Preflight

When archie-core's `[[repos]]` entry sets `ecosystem = "node"`, the daemon runs
`node --version` as the preflight check before any agent stage.

## Recommended gate configuration

```toml
[[repos]]
owner = "sam"
name = "my-node-project"
ecosystem = "node"

[[repos.gate]] = ["pnpm", "run", "lint"]
[[repos.gate]] = ["pnpm", "run", "typecheck"]
[[repos.gate]] = ["pnpm", "run", "test"]
```

Or for npm projects:

```toml
[[repos.gate]] = ["npm", "run", "lint"]
[[repos.gate]] = ["npm", "run", "build", "--", "--noEmit"]
[[repos.gate]] = ["npm", "test"]
```

## Test glob

The default test glob for Node is `*.test.ts`. The TDD workflow's repro-tests
stage write-protects files matching this glob during the fix stage.

Override with `test_glob = "**/*.spec.ts"` for projects using that convention,
or `test_glob = "__tests__/**/*.ts"` for Jest-style test directories.

## Package management

arcie-core detects the lockfile:
- `pnpm-lock.yaml` → use `pnpm`
- `package-lock.json` → use `npm`
- `yarn.lock` → use `yarn`

Install before gate:
```bash
pnpm install --frozen-lockfile
```

## Common tools

| Tool | Purpose | Config file |
|---|---|---|
| `eslint` | Linting | `eslint.config.mjs` |
| `prettier` | Formatting | `.prettierrc` |
| `tsc --noEmit` | Type checking | `tsconfig.json` |
| `vitest` | Testing | `vitest.config.ts` |
| `jest` | Testing (legacy) | `jest.config.ts` |

## Common gate failures

### eslint
- `no-unused-vars`: remove unused import/variable
- `@typescript-eslint/no-explicit-any`: add proper type
- Import order: let eslint fix it with `--fix`

### tsc / typecheck
- `TS2345`: type mismatch — fix the argument or the signature
- `TS2339`: property doesn't exist — check the type definition
- `Cannot find module`: check the import path or install the package

### vitest / jest
- Test timeout: the test is hanging — check async/await, mocks, or network calls
- Snapshot mismatch: the output changed — update the snapshot if the change is intentional
- Assertion failure: fix the code, not the test
