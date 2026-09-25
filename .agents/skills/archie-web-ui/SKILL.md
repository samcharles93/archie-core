---
name: archie-web-ui
description: "Develop, review, or redesign archie-core's dashboard web UI (ui/). Load when adding or changing Vue components, pages, views, styling, theming, layout, or interaction patterns under ui/src; when auditing UI accessibility or contrast; or when porting design prototypes (e.g. React mockups) into the Vue dashboard. Carries stack facts, design-system rules, and the operational-tool tone required here."
---

# Web UI development (ui/)

Archie's dashboard is an **operational supervision tool** for senior developers
watching an autonomous orchestrator: quiet, dense, scannable, work-focused.
Not editorial, not marketing, not playful. Every rule below serves that.

## Stack facts (do not fight these)

- Vue 3 SFCs + TypeScript, Vite, Pinia, vue-router.
- Tailwind CSS v4 via `@tailwindcss/vite`; `@custom-variant dark` keyed on the
  `[data-theme]` attribute; shadcn-vue + reka-ui for primitives;
  `@lucide/vue` for icons; IBM Plex Sans for UI, IBM Plex Mono for every
  machine value (Graphite theme, `docs/design/ui-redesign/`).
- `ui/src/components/ui/` is CLI-installed only; never hand-edit it. Order of
  preference for a missing component: stock shadcn-vue (`npx shadcn-vue add`;
  reka-ui names differ, e.g. chip input is Tags input), another shadcn-vue
  registry (e.g. inspira-ui), then a new item in the sibling
  `archie-component-registry` repo, installed from its `public/r/<item>.json`.
  The CLI prompts to overwrite stock primitives an item depends on: answer no.
  To update an installed registry item, delete its `components/ui/<item>/`
  folder and add it again; never pass `--overwrite`, which also rewrites the
  stock components listed in its `registryDependencies`.
  If `npm install` fails with `EALLOWSCRIPTS`, run the CLI with
  `NPM_CONFIG_USERCONFIG=/dev/null`.
- Features live in `ui/src/<feature>/` with colocated `.js`/`.css`. Extract a
  helper to `ui/src/base/` only on the **second distinct consumer**.
- `ui/dist/` is committed and generated — never hand-edit it, never ignore it
  (see the header comment in `ui/src/style.css` for why builds are
  content-coupled to the bundle).
- Gates: `task ui` (build), `task test:ui` (node --test over DOM-building
  primitives in `ui/test/`), `vue-tsc` via `npm run typecheck`. `task check`
  includes all of them.
- The dashboard is a LAW asset built from `ui/dist` — a Go-side embed. A UI
  change is not done until `task ui` regenerates it and the commit carries it.

## Design-system rules

- **Tokens only.** Semantic CSS variables from `ui/src/style.css`
  (`--primary`, `--ok`, `--muted`, ...). Never a raw hex in a component, never
  a hue-named token. Dark is default; light defines the same variable set and
  is a first-class theme, not an afterthought. Density is a single knob
  (`[data-density="compact"]` tightens the spacing scale) — do not add
  per-component density conditionals.
- **Icons, not text-as-decoration.** Buttons for clear commands use lucide
  icons (icon-only where the symbol is familiar, with a tooltip naming it);
  segmented controls for modes, toggles/checkboxes for binary settings,
  selects/menus for option sets, tabs for views.
- **Cards 8px radius or less; no cards inside cards;** page sections are
  full-width bands or unframed layouts, not floating cards. No gradient orbs,
  bokeh blobs, or decorative backgrounds.
- **Stable dimensions.** Tables, tiles, toolbars, sparklines, and icon buttons
  get fixed/constrained sizing so dynamic content (long titles, loading
  states, big numbers) cannot shift layout. Text never overflows its
  container on any viewport; wrap or downsize before it can.
- **No viewport-scaled font sizes, no negative letter-spacing.**
- **No one-note palettes.** If a screen reads as a single hue family
  (e.g. all-violet), rebalance with neutrals and status colours rather than
  more of the accent.
- **No in-app prose explaining the app** — no help text describing features,
  keyboard shortcuts, or how to use a control. If a control needs explaining,
  redesign the control.
- **No landing pages.** Every route opens the working experience directly.

## Tone & content

- Match copy to the domain: state names, gate results, timings, token counts —
  concrete operational nouns, not conversational flourishes. A header states
  system status ("Cluster operational · 4 awaiting intervention"), not a
  greeting.
- Every human-actionable state (parked, gate failed, needs review) shows the
  reason inline and links to the exact place to act on it (the failing log,
  the gate output) — never just a status label.
- High-frequency background telemetry is grouped/summarised in activity feeds
  ("session-memory · 24 background cycles"), with high-signal events
  (stage finished, gate failure, task parked) visually elevated.

## Accessibility bar

- WCAG AA minimum on all text: secondary/metadata labels must clear 4.5:1
  against their actual background — audit the token, not the element. If a
  muted token fails, fix the token value (one file), not individual spots.
- Log output, hashes, and code are monospace in a strict grid; do not put
  rounded pill badges inside raw log lines (breaks monospace alignment).
  Truncated stack traces fold into expandable blocks.
- Controls that filter or toggle (log level, wrap, follow, search) live in a
  sticky toolbar inside the pane they affect.

## Porting design prototypes

External mockups (Gemini, Figma, React `.tsx` prototypes) are **specs, not
code**. This app is Vue; translate layout, hierarchy, and interaction
decisions into SFCs using shadcn-vue primitives and existing tokens. Mock data
in a prototype proves nothing — verify the pattern against real task/log/diff
shapes before building around it. A prototype proposing new information
architecture (e.g. splitting the task view into a master-detail pane) is an
open design question for the maintainer. Web UI design is not tracked in
`docs/prds/`; record the decision on the bead and in `docs/design/`.
