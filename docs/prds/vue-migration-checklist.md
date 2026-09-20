# Preact → Vue 3 + shadcn-vue migration checklist

**Status:** In progress
**Date:** 2026-09-20
**Bead:** `archie-core-ocfm` (navigation restructure, folded in below)
**Bootstrap commit:** `e18fb8b2`

The Preact dashboard was deleted, not ported incrementally. This is the list of
what has to exist again, what must not be lost on the way, and the contracts
that break silently if missed.

Deleted source is recoverable at `git show e18fb8b2^:ui/src/<path>`.

## Invariants that outlive the framework

These are not features; they are things other processes depend on. Breaking one
does not show up as a broken page.

- [ ] **Embed contract.** `ui/embed.go` embeds a committed `ui/dist`. The build
      must keep `base: "./"` and unhashed `assets/index.{js,css}`. Already set
      in `vite.config.ts`; do not let a later Vite change reintroduce hashing.
- [ ] **Route registry.** `internal/gateway/dashboard_tools.go` hand-copies the
      route table and `TestDashboardPagesRegistryCoversEveryRoute` parses
      `ui/src/router/index.ts` to hold them in step. Adding a route without a
      registry entry fails the gate; that guard caught a stale `/chat` entry
      earlier today. Keep the `const routes = [` / one-per-line / `nav: false`
      shape the parser expects.
- [ ] **Hash routing.** Every existing bookmark, dashboard link and
      agent-issued navigation is `#/tasks`. `createWebHashHistory`, already set.
- [ ] **Capability gating.** `GET /api/capabilities` returns which `section`s
      the serving process can back; a route whose section is absent must be
      hidden, not rendered empty. Was `capabilities.jsx` +
      `hiddenRoutes(caps, routes)`. Fail open: unknown or failed response hides
      nothing.
- [ ] **Single fetch path.** Every call went through one `send()` in
      `base/api.jsx` so the CSRF header, content type, timeout and `ApiError`
      shape lived in one place. A test used to fail if a second `fetch()`
      appeared there. Port the constraint, not just the functions.
- [ ] **SSE lifecycle.** `subscribeLogs` and the dashboard event stream must be
      torn down on unmount or the app leaks a connection per navigation. Preact
      did this with an `archie:teardown` event; Vue should use `onUnmounted`.

## Phase 1 — Foundation

- [ ] **Theme.** `css/tokens.css` was a complete two-theme semantic token set
      (`--fg-subtle`, `--accent`, `--ok/warn/danger`, surfaces, shadows). Map it
      onto Tailwind v4's `@theme` so shadcn components inherit it. Do not keep
      both systems.
- [ ] **`color-scheme`.** `[data-theme="dark"] { color-scheme: dark }` and the
      light equivalent. Without it the UA paints light scrollbars and light
      native controls inside the dark app. Fixed today; trivially lost.
- [ ] **Contrast values.** Carry the corrected tokens, not the originals:
      `--fg-subtle` `#7d8b9e` dark / `#636f82` light, and `--accent-fg`
      `#0d1014` on the dark accent. The originals failed WCAG AA at 3.37:1,
      2.72:1 and 3.2:1. The accent itself is unchanged and is also the link
      colour.
- [ ] **API client** (`base/api.jsx` → `src/lib/api.ts`). ~45 methods; list
      recoverable from the deleted file. Keep `ApiError` carrying `status`, and
      the 401/4xx/5xx classification callers branch on.
- [ ] **App shell.** Topbar, `.shell` frame, main outlet. Note `.shell` has
      `backdrop-filter`, which makes it a containing block for `position:
      fixed` — the chat launcher must render outside it or it anchors to the
      scrolling shell.
- [ ] **Theme toggle** and its persistence.

## Phase 2 — Shared primitives

Old hand-built components and their shadcn-vue replacements. Take the
replacement unless the behaviour is genuinely bespoke.

| Was | Becomes |
|---|---|
| `base/pill.jsx` | `Badge`, with the status-token variants |
| `base/statTile.jsx` | `Card` composition |
| `base/icons.jsx` | `lucide-vue-next` (the set already matched a 24-grid stroke system) |
| `base/gauge.jsx` | bespoke SVG — port as-is |
| `base/log-row.jsx` | bespoke — port as-is |
| `base/format.jsx`, `base/task-meta.jsx` | plain modules, port as-is |
| `css/table.css` | `Table` |
| `css/empty.css` | `Empty` |
| `css/button.css` | `Button` |

- [ ] **Copy verbatim.** These five are framework-free and need only a rename:
      `dashboard/activity-detail.js`, `chat/command-scroll.js`,
      `workflows/workflow-rows.js`, `base/stream-state.js`,
      `logs/logs-empty.js`. Each encodes a bug fixed today; re-deriving them by
      hand is how the bugs come back.
- [ ] `base/uuid.jsx` → `src/lib/uuid.ts`.
- [ ] `routing.jsx` is superseded by vue-router. Do not port it.

## Phase 3 — Pages

In dependency order. Each is `git show e18fb8b2^:ui/src/<folder>/`.

- [ ] **Dashboard** (5 files). Health row, Throughput tiles, Token outlook,
      Live activity table.
- [ ] **Tasks** (13 files — the largest). List, filters, row actions, detail
      page with tab bar, stage rail, changed files, attempt config, task logs,
      timeline, debug view.
- [ ] **Chat** (8 files). See Phase 5; this one carries the most recent work.
- [ ] **Configuration** (5 files). Currently one ~4000px page; the approved
      design splits it into `/system/*` routes — see
      `docs/prds/dashboard-navigation-groups.md`. Port into that shape directly
      rather than porting the monolith and splitting later.
- [ ] **Logs** (3 files). Filters, live tail, pause, the three empty states.
- [ ] **Event inspector / captures** (3 files).
- [ ] **Field mappings** (3 files), including the mapping editor.
- [ ] **Playbook bindings** (3 files), including the binding editor.
- [ ] **Workflows** (3 files).
- [ ] **Skills**, **Curators**, **Channels** (2 files each). Capability-gated
      and hidden on the reference deployment, so they are easy to forget and
      easy to ship broken.

## Phase 4 — Navigation restructure

Approved in `docs/prds/dashboard-navigation-groups.md`; build it during the
port rather than porting the flat bar first.

- [ ] Five top-level items: Dashboard, Work, Agent, Events, System.
- [ ] Rename under their group: Inspector, Mappings, Bindings.
- [ ] Per-item descriptions in the dropdown, not hover tooltips.
- [ ] Configuration splits into `/system/{status,appearance,tasks,models,repos,advanced}`.
- [ ] `/settings` redirects to `/system/status` — it is in bookmarks and in the
      Go route registry.
- [ ] Add Logs to the nav. It was never in it.
- [ ] Update `dashboard_tools.go` for every new route in the same change.

## Phase 5 — Behaviour shipped today that a naive port loses

All of this landed in the last few hours and exists only in the deleted tree.

- [ ] **Chat is a FAB, not a route.** No `/chat`, no nav entry, no topbar icon.
- [ ] **The panel grows out of the launcher**: collapsed to a pill over the
      button, expanding with scale, border-radius and position interpolating
      together; contents fade in after the shape arrives.
- [ ] **No scrim, no scroll lock.** The page stays readable and clickable.
- [ ] **No close button** — the FAB is the only toggle, and stays visible on
      mobile where the panel becomes a sheet.
- [ ] Header shows the open conversation's name (`panelTitle`).
- [ ] New chat = `message-circle-plus`, in the header.
- [ ] Slash palette is the only command surface; it floats above the input and
      scrolls to keep the arrow-key selection visible.
- [ ] Settings popover closes on outside pointerdown and focus-out.
- [ ] Stop renders only while streaming; composer stays writeable and focused
      during a send.
- [ ] Bubbles carry no speaker label.
- [ ] **Topbar cannot clip.** The nav is the only shrinkable region; search
      collapses below 1500px; nav wraps below 1270px. Previously 106px of the
      utility cluster was unreachable between 1221px and ~1582px.
- [ ] **Live activity detail is clamped** to one line with the full payload on
      the title. Unclamped it made the dashboard 7,481px tall.
- [ ] **HEALTH cards take their own height** (`align-items: start`).
- [ ] **Workflow rows merge** definitions with statistics.
- [ ] **Log stream reports `unavailable`** from `readyState`, not a permanent
      "reconnecting".
- [ ] **Unconfigured capabilities say so** (501 → "Not configured on this
      deployment") rather than rendering nothing.
- [ ] **Operator cards on Configuration**: update install/defer, dangerous
      action approvals. Both absent when their endpoint 501s.

## Known-open, carry forward

Open beads against the old UI that the port should satisfy rather than
reintroduce: `1q89` (tasks column priority, wrapping row actions), `to2y`
(three empty-state treatments; Field mappings dead-ends), `gl96` (Logs missing
from nav), `86au` (gauge `aria-label` announces an unrounded percentage),
`6m8r` (mobile KPI cards push content below the fold; 89 sub-44px targets),
`ggqo` (config page width — moot once Configuration splits), `3tij`
(`/api/workflows` serves no definitions, so "Start work" never renders),
`oif3` (Reload Telegram, blocked on `ry4k`).

## Not doing

- Porting the 43 test files. Tests return when there is behaviour worth pinning
  rather than a port to re-assert. `task test:ui` is a `vue-tsc` typecheck
  meanwhile.
