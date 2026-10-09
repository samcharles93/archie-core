# UI agent rules

These rules supplement the repository root's AGENTS.md. Its authority, service
boundaries, security invariants, Beads workflow and commit gate still apply.
CLAUDE.md points here so both agents use one policy.

## Scope

The dashboard is Vue 3, TypeScript, Pinia and Vue Router, with Tailwind and
vendored shadcn-vue components backed by Reka UI. It talks to the control plane
through src/lib/api.ts. The server owns contracts, authorization and durable
state; the UI presents them and submits operator choices.

- Fix broken behavior with the smallest complete change. Finish existing
  features before adding new ones. Do not add an abstraction or dependency for
  a few lines of code.
- Read the affected callers and existing components before editing. Keep
  feature state and behavior in its feature folder; shared code must have real
  consumers. Do not extract production helpers solely to make them testable.
- Preserve server vocabulary and typed wire values. Do not invent local status,
  action or model catalogs when the server supplies them.
- Keep requests in src/lib/api.ts and shared live updates in the existing store.
  Preserve its authentication, CSRF, timeout and error handling.
- Keep unavailable, loading, empty and failed states distinct. Show failures
  where they occur; an unreadable list must not look like an empty one.
- Async views must discard stale responses after their task, attempt or route
  changes. A setting must save through the API and show its applied state.

## Components and interaction

- Reuse components/ui and surrounding composition patterns before writing new
  markup. Keep vendored components local; use npx shadcn-vue@latest for CLI work
  instead of adding the generator and its dependency tree to the application.
- Use semantic theme tokens, existing variants and cn() for conditional classes.
  Support both themes, compact density and reduced motion.
- Keep controls compact and contextual. Use on-demand panels and inline
  hover/focus icon actions rather than permanent chrome. Do not repeat labels
  in explanatory text or add controls without working behavior.
- Preserve keyboard operation, visible focus, accessible names, dialog titles
  and table semantics. A hover action must also be reachable by keyboard.
- Keep filtering and pagination consistent with the server. State limitations
  such as sorting only loaded rows when they affect the operator's decision.

## Tests

The default is no new test. Add one for a reproduced bug or a change to a
security boundary or workflow contract, following the root policy. A regression
must fail on the relevant assertion before the fix.

- Test observable behavior with realistic inputs. Do not test source text,
  imports, wiring, constants, defaults, type shapes, CSS classes, copied labels,
  fakes or call order. A fake may support a behavior test; it is not the subject.
- Use one table-driven test per behavior. Extend its cases rather than adding
  tests for each branch. Retain meaningful edge cases; reducing the count alone
  is not a cleanup.
- Keep tests fast and cheap to maintain. Test helpers and fixtures belong in
  test/, never in production code solely for tests.
- For mirrored control-plane validators, extend the shared fixture at
  internal/app/controlplane/testdata/dashboard-validators.json rather than
  duplicating accept/reject cases in UI-only tests.
- Use browser checks for rendered behavior. Matching a Vue file with a regex
  does not verify what the user sees. Mocked API checks verify UI behavior, not
  live service integration; report that distinction.

## Verification and delivery

```bash
npm test               # behavior tests
npm run typecheck      # TypeScript and Vue checks
npm run build          # rebuild the embedded dashboard
npm audit              # after dependency changes
```

- Run task check from the repository root before committing; stage only the
  task's own paths. Follow the root's commit and push rules.
- ui/dist is embedded and committed. Regenerate it for source changes; never
  edit it by hand. Test-only edits do not need new dashboard assets.
- Reuse a running dev server for browser checks. Verify changed interactions,
  not just a screenshot or successful HTTP response.
- Track work and follow-ups in Beads. Keep rules here only when they change a
  decision; do not add task history or Markdown TODO lists.
