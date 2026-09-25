# Dashboard port: what the agent delivers

**Status:** Approved
**Date:** 2026-09-20
**Beads:** `archie-core-tavn` (epic), `archie-core-ocfm` (navigation)
**Reads with:** `docs/prds/vue-migration-checklist.md` (the item-by-item)

This is the standard the port is held to. The checklist says what to port; this
says how, and what counts as done. It exists because the first attempt ported
the flat topbar verbatim and invented a palette, both of which had already been
decided elsewhere.

## The deliverable

The Preact dashboard, rebuilt in Vue 3 with shadcn-vue, feature complete
against the checklist, gate clean, with `ui/dist` rebuilt and committed in the
same commit as the source it was built from.

## Non-negotiables

- **Vue 3 + shadcn-vue (Reka UI, Tailwind v4, Lucide).** A component that
  exists in shadcn-vue is not hand-rolled. Add it with the CLI
  (`npx shadcn-vue@latest add <name>`), do not paste it in.
- **History routing.** `createWebHistory`, `base: "/"`. No hash routing.
  `src/legacy-hash-redirect.ts` handles old `#/path` links and stays.
- **No test port.** 43 Preact test files are deliberately dropped.
  `task test:ui` is a `vue-tsc` typecheck. Write a test when there is a real
  failure to pin, not to re-assert a port.
- **Do not downgrade TypeScript.** The target is TS 7 native, not TS 5.
- **`ui/dist` is law.** Never hand-edit it, never omit it, never revert it.
- **`task check` is the gate.** Not an assembled substitute for it.
- **Beads, not markdown TODOs**, for anything that outlives the change.
- **Conventional commits**, scoped `feat(webui):`. Commit gate-clean work
  without asking. Do not push without being told.

## Componentisation

One component, one job, one file. A surface is composed from named components,
not assembled inside a single large file. The topbar is the worked example and
the required shape:

```
components/topbar/
  Topbar.vue          composes the header, owns nothing else
  Brand.vue           Logo + BrandName, links to /
  Logo.vue
  BrandName.vue
  Nav.vue             reads the nav model, renders links and groups
  NavTrigger.vue      the shared top-level control, link or dropdown trigger
  NavGroup.vue        one dropdown
  NavItem.vue         one item inside a dropdown, with its tooltip
  SearchBar.vue
  SupportLink.vue
  ThemeToggle.vue
  ProfileDropdown.vue
```

The same rule applies to every page ported after it. If a file is doing the
work of four named things, it is four files. Data and behaviour that more than
one component needs lives in `src/lib/`, not passed down through a chain of
props.

## Ordering

Build the navigation restructure during the port, not after it. Porting the
flat eleven-item bar first and restructuring later means writing the topbar
twice and touching the Go route registry twice. The approved design is five
top-level items (Dashboard, Work, Agent, Events, System), items renamed because
their group carries the context, per-item descriptions shown as hover tooltips,
Configuration split into `/system/*`, `/settings` kept as a redirect, and Logs
finally given a nav entry.

## Design authority

- The approved documents win. Where a document decides something, implement it
  rather than re-deciding it.
- **Do not invent a visual direction.** Palette, typography and density are the
  maintainer's call. A palette change is its own request, never a side effect
  of a port.
- Contrast is AA against every surface the foreground lands on, hover states
  included. The corrected values (`--fg-subtle`, the dark
  `--primary-foreground`) came from a measured failure and must survive.
- `color-scheme` follows the theme so the UA paints controls, scrollbars and
  overscroll to match.

## Code style

Comment the non-obvious why: an invariant, a trap, a measurement, a contract
another file depends on. Do not narrate what the code plainly does, do not
explain a choice that was never in question, and do not write migration phase
history into a diff. That history belongs in the commit message.

## Done, per surface

1. Renders with real data against `task dev`, not just typechecks.
2. Keyboard reachable, focus visible, and the nav announces where you are.
3. Works at 1440x900 and at 720px wide, with nothing clipped and no horizontal
   page scroll.
4. Empty, loading, failed and unconfigured (501) states all say something true.
   A section that cannot load says so; it does not render blank.
5. `task check` passes, `ui/dist` rebuilt, committed together.
