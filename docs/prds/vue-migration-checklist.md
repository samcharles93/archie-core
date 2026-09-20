# Preact → Vue 3 + shadcn-vue migration checklist

**Status:** In progress
**Date:** 2026-09-20
**Bead:** `archie-core-tavn` (epic), `archie-core-ocfm` (navigation restructure)
**Bootstrap commit:** `e18fb8b2`

The Preact dashboard was deleted, not ported incrementally. This is the list of
what has to exist again, what must not be lost on the way, and the contracts
that break silently if missed.

Deleted source is recoverable at `git show e18fb8b2^:ui/src/<path>`. Every
claim below was cross-checked against that tree; where a number is a live
measurement rather than something recoverable from git, it says so.

## Invariants that outlive the framework

These are not features; they are things other processes depend on. Breaking one
does not show up as a broken page.

- [ ] **Embed contract.** `ui/embed.go` embeds a committed `ui/dist`. The build
      must keep `base: "./"` and unhashed `assets/index.{js,css}`. Already set
      in `vite.config.ts`; do not let a later Vite change reintroduce hashing.
- [ ] **Route registry.** `internal/gateway/dashboard_tools.go` hand-copies the
      route table and `TestDashboardPagesRegistryCoversEveryRoute` parses
      `ui/src/router/index.ts` to hold them in step. The parser needs
      `const routes = [` on its own line, `path: "..."` on a single line per
      route, and `nav: false` on any entry the registry should skip. That guard
      caught a stale `/chat` entry earlier today.
- [ ] **Mutation headers.** Every POST/PATCH/DELETE must send **both**
      `X-Archie-CSRF: 1` and `Content-Type: application/json`. The server
      returns 403 without the first and 415 without the second
      (`internal/webui/api_tasks.go:376`, `authorizeTaskMutation`). GET sends
      neither. This is the single most likely thing to silently break every
      write after the port.
- [ ] **Hash routing.** Every existing bookmark, dashboard link and
      agent-issued navigation is `#/tasks`. `createWebHashHistory`, already set.
- [ ] **Capability gating.** `GET /api/capabilities`
      (`internal/webui/api_capabilities.go:20`) returns a `sections` map of ten
      booleans: chat, logs, skills, curators, channels, captures, mappings,
      bindings, and workflows/settings which are always true. A route whose
      section is false must be hidden, not rendered empty. **Fail open**: on a
      failed or null response hide nothing — the old client used a bare
      `.catch(() => {})` for exactly this. A page that says "unavailable" is
      recoverable; a nav entry that vanished is not.
- [ ] **`LOG_LEVELS`** in `base/log-row.jsx` is a wire contract, not a display
      list: the CSV level filter is matched server-side by `splitCSV` in
      `internal/webui/api_tasks_logs.go`. A value mismatch breaks filtering
      with no error.
- [ ] **SSE lifecycle.** Three streams, all `id: <n>\ndata: <json>\n\n` except
      chat which omits the id:
      `/events` (task lifecycle, `sse.go:37`, supports `?since=` and
      `Last-Event-ID` resume), `/api/logs/stream` (`api_logs.go:80`, same
      resume protocol), `/api/chat/stream` (`api_chat.go:399`, frames typed
      delta/tool/media/navigate). Each must be closed on unmount or the app
      leaks a connection per navigation. Preact used an `archie:teardown`
      event; Vue should use `onUnmounted`.

> **On tests.** Per Sam's call, the 43-file suite was not ported and tests
> return when there is behaviour worth pinning. Flagging one exception for when
> that moment comes: the deleted `ui/test/api-client.test.js` classified every
> API method as mutation/read/stream/url and asserted the right headers per
> class. It is the only cheap guard on the CSRF contract above. (It did **not**
> text-scan for a second `fetch()` — that check was deliberately removed
> upstream for testing the file's wording rather than its behaviour.)

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
      2.72:1 and 3.2:1 (independently recomputed). The accent itself is
      unchanged and is also the link colour.
- [ ] **API client** (`base/api.jsx` → `src/lib/api.ts`). **48 methods plus two
      SSE subscribers.** Keep `ApiError` carrying `status`, and the
      401/4xx/5xx classification callers branch on. `GET /api/bindings/{id}`
      and `GET /api/mappings/{id}` exist server-side but were never called —
      do not add clients for them without a reason.
- [ ] **App shell** — the real source is `main.jsx` (410 lines), not just CSS.
      Beyond the topbar and outlet it carries:
  - [ ] **Jump-to search**: Enter navigates to the first nav label matching the
        typed prefix; Escape closes the field and calls `preventDefault` so it
        does not also close the chat panel, which has its own Escape handler.
  - [ ] **`soon: true` nav entries** — greyed, labelled, non-navigable. An
        affordance, not dead code.
  - [ ] **`loadTaskMeta()` at boot**, which re-renders the mounted route once
        `/api/task-meta` lands so freeze-dried defaults are replaced.
  - [ ] **Route keying**: keyed on path + params but deliberately **not** the
        query string, so a query-only change (a task-detail tab switch) does
        not remount and lose state, while two different `:id`s do get fresh
        instances. vue-router needs an equivalent `:key` strategy or this
        regresses silently.
  - [ ] `.shell` has `backdrop-filter`, making it a containing block for
        `position: fixed` — the chat launcher must render outside it.
- [ ] **CSS inventory.** Eleven files, of which only four map onto shadcn
      primitives (`table.css`→Table, `empty.css`→Empty, `button.css`→Button,
      `tokens.css`→theme). The rest carry behaviour with no other home:
      `layout.css` (498 lines — the shell, topbar, nav, the whole chat dock and
      morph, and every responsive breakpoint Phase 5 describes in prose; port
      **from this file**, do not reverse-engineer it), `card.css`
      (`.card`/`.grid-2`/`.grid-4`, used on nearly every page), `page.css`
      (`.page-head`/`.page-title`/`.page-sub`/`.page-actions`, every page),
      `pill.css` (the ok/warn/danger/info/idle colour definitions a Badge port
      must match), `base.css` (`:focus-visible` ring, `.sr-only`),
      `responsive.css` (the global `prefers-reduced-motion` override — an
      accessibility invariant), `_main.css` (import order only).
- [ ] **Theme toggle** and its persistence.

## Phase 2 — Shared primitives

| Was | Becomes |
|---|---|
| `base/pill.jsx` | `Badge`, with the status-token variants from `pill.css` |
| `base/statTile.jsx` | `Card` composition **plus** its `Sparkline` (inline SVG polyline with its own min/max/step math) and trend-arrow logic, where `goodDirection` distinguishes "up is good" from "up is bad" |
| `base/icons.jsx` | `lucide-vue-next` for most, but `dashboard`, `mappings`, `bindings`, `curators` and `memory` are bespoke glyphs with no 1:1 Lucide equivalent. Swapping them changes the drawing — a design call, not a rename |
| `base/gauge.jsx` | bespoke SVG, port as-is — **exports two components**, `Gauge` and `SegmentBar` (proportional multi-segment bar plus legend, used for budget composition). Easy to port only the one the filename names |
| `base/log-row.jsx` | bespoke, port as-is; see the `LOG_LEVELS` wire contract above |
| `base/format.jsx` | genuinely pure, port as-is |
| `base/task-meta.jsx` | **not** a plain module: module-level mutable cache, async fetch, and the boot-time re-render trigger above |

- [ ] **Copy verbatim.** These five are framework-free and need only a rename:
      `dashboard/activity-detail.js`, `chat/command-scroll.js`,
      `workflows/workflow-rows.js`, `base/stream-state.js`,
      `logs/logs-empty.js`. Each encodes a bug fixed today; re-deriving them by
      hand is how the bugs come back.
- [ ] `base/uuid.jsx` → `src/lib/uuid.ts`.
- [ ] `routing.jsx` is superseded by vue-router. Do not port it.

## Phase 3 — Pages

In dependency order. Each is `git show e18fb8b2^:ui/src/<folder>/`. File counts
verified against the deletion diff.

- [ ] **Dashboard** (5 files). Health row, Throughput tiles, Token outlook,
      Live activity table. Also the time-of-day `greeting()` and
      `taskIDForEvent`, the click-through from an activity row to its task.
- [ ] **Tasks** (13 files — the largest). List, filters, row actions, detail
      page with tab bar, stage rail, changed files, attempt config, task logs,
      timeline, debug view. Two interaction contracts that are not visual:
      detail `tab`/`attempt` state lives in the query string and is written
      with `history.replaceState`, **not** `location.hash =`, so a tab click
      does not remount and refetch; and the list has a reduced-motion-aware
      scroll-to-row reveal for deep links.
- [ ] **Chat** (8 files, `chat.jsx` alone is 810 lines). Phase 5 covers the
      launcher chrome; the content features are separate and none of them are
      chrome: session list and switching, persona selector, provider and model
      selectors (also driven by `/model` and persona slash commands), and
      **inline tool-call rendering in the transcript** — name, parameters,
      summary, and success-or-failure per call.
- [ ] **Configuration** (5 files). The approved design splits it into
      `/system/*` routes — see `docs/prds/dashboard-navigation-groups.md` — so
      port into that shape directly rather than porting the monolith first.
      Content beyond layout: `RepositoriesCard`'s inline-editable cells
      (PATCH-on-blur with keyboard handling), `ModelsAndProvidersCard`,
      `ProvenanceCard`, `LifecycleCard`, and the **editable-vs-read-only dual
      mode** — `editable={false}` when the serving process only has a published
      config snapshot and the PATCH routes answer 503 (`archie-core-ymut`).
      That is architecture, not styling.
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
- [ ] Preserve the `soon:` entry treatment.
- [ ] Update `dashboard_tools.go` for every new route in the same change.

## Phase 5 — Behaviour shipped today that a naive port loses

All of this landed in the last few hours and exists only in the deleted tree.
The implementation source for nearly all of it is
`git show e18fb8b2^:ui/src/css/layout.css` plus `main.jsx` and `chat/chat.jsx`.

- [ ] **Chat is a FAB, not a route.** No `/chat`, no nav entry, no topbar icon.
- [ ] **The panel grows out of the launcher**: collapsed to a pill over the
      button, expanding with scale, border-radius and position interpolating
      together; contents fade in after the shape arrives.
- [ ] **No scrim, no scroll lock.** The page stays readable and clickable.
- [ ] **No close button** — the FAB is the only toggle, and stays visible on
      mobile where the panel becomes a sheet.
- [ ] Escape closes the panel **and returns focus to the launcher**; the dock
      carries `aria-expanded`/`aria-controls`/`aria-hidden`.
- [ ] Header shows the open conversation's name (`panelTitle`).
- [ ] New chat = `message-circle-plus`, in the header.
- [ ] Slash palette is the only command surface; it floats above the input and
      scrolls to keep the arrow-key selection visible.
- [ ] Settings popover closes on outside pointerdown and focus-out.
- [ ] Stop renders only while streaming; composer stays writeable and focused
      during a send.
- [ ] Bubbles carry no speaker label.
- [ ] **Topbar cannot clip.** The nav is the only shrinkable region; search
      collapses below 1500px; nav wraps below 1270px; sheet mode at 720px;
      edge-to-edge at 900px. Previously the utility cluster was unreachable
      between 1221px and ~1582px: measured at 1440x900, `.topbar-end` ended at
      x=1515 against a shell edge of 1409, so 106px carrying the avatar, theme
      toggle and documentation button was clipped with no scroll affordance.
      That figure is a live measurement, not recoverable from git.
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
  meanwhile. See the note under Invariants for the one exception worth
  reconsidering first.
