# Preact → Vue 3 + shadcn-vue migration checklist

**Status:** Approved
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
      must keep unhashed `assets/index.{js,css}`: dist/ lives in git, and
      hashed names orphan a file on every rebuild.
- [ ] **Route registry.** `internal/gateway/dashboard_tools.go` hand-copies the
      route table and `TestDashboardPagesRegistryCoversEveryRoute` parses
      `ui/src/router/index.ts` to hold them in step. The parser needs
      `const routes = [` on its own line, `path: "..."` on a single line per
      route, and `nav: false` on any entry the registry should skip. That guard
      caught a stale `/chat` entry earlier.
- [ ] **Mutation headers.** Every POST/PATCH/DELETE must send **both**
      `X-Archie-CSRF: 1` and `Content-Type: application/json`. The server
      returns 403 without the first and 415 without the second
      (`internal/webui/api_tasks.go`, `authorizeTaskMutation`). GET sends
      neither. This is the single most likely thing to silently break every
      write after the port.
- [x] **History routing.** `createWebHistory`, `base: "/"`. Legacy `#/path`
      links are rewritten by `src/legacy-hash-redirect.ts`. The chat navigate
      chip should emit `/tasks`; the Go side already stores bare paths.
- [ ] **Capability gating.** `GET /api/capabilities`
      (`internal/webui/api_capabilities.go`) returns a `sections` map of ten
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
      `/events` (task lifecycle, `sse.go`, supports `?since=` and
      `Last-Event-ID` resume), `/api/logs/stream` (`api_logs.go`, same
      resume protocol), `/api/chat/stream` (`api_chat.go`, frames typed
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

- [x] **Theme.** `css/tokens.css` was a complete two-theme semantic token set
      (`--fg-subtle`, `--accent`, `--ok/warn/danger`, surfaces, shadows). Mapped
      onto Tailwind v4's `@theme` in `src/style.css`; the second system was not
      kept.
- [x] **`color-scheme`.** `[data-theme="dark"] { color-scheme: dark }` and the
      light equivalent. Without it the UA paints light scrollbars and light
      native controls inside the dark app. Fixed; trivially lost.
- [x] **Contrast values.** The violet palette moved these off the values pinned
      here, so they were recomputed against its own backgrounds instead of
      carried: `--fg-subtle` `#A99DBD` dark (7.67:1, AAA) / `#6E6577` light
      (4.87:1, AA), and `--fg-muted` `#B9AECB` / `#635B6B` (9.28:1 and 5.70:1).
      The invariant the pin existed for holds; the values are the palette's.
- [x] **API client** (`base/api.jsx` → `src/lib/api.ts`). Keep `ApiError`
      carrying `status`, and the 401/4xx/5xx classification callers branch on
      (`classifyActionError`). `GET /api/bindings/{id}` and
      `GET /api/mappings/{id}` exist server-side but were never called — do not
      add clients for them without a reason.
- [x] **App shell** — the real source is `main.jsx` (410 lines), not just CSS.
      Beyond the topbar and outlet it carries:
  - [x] **Jump-to search**: Enter navigates to the first nav label matching the
        typed prefix; Escape closes the field and calls `preventDefault` so it
        does not also close the chat panel, which has its own Escape handler.
  - [x] **`soon: true` nav entries** — greyed, labelled, non-navigable. An
        affordance, not dead code.
  - [x] **`loadTaskMeta()` at boot**, which re-renders the mounted route once
        `/api/task-meta` lands so freeze-dried defaults are replaced.
  - [x] **Route keying**: keyed on path + params but deliberately **not** the
        query string, so a query-only change (a task-detail tab switch) does
        not remount and lose state, while two different `:id`s do get fresh
        instances.
  - [x] `.shell` has `backdrop-filter`, making it a containing block for
        `position: fixed` — the chat launcher must render outside it.
- [ ] **CSS inventory.** `layout.css` was reassessed rather than ported whole.
      The command bar is a Vue component styled with Tailwind like every other
      surface inside the shell, so it emits none of that file's `.topbar`,
      `.nav`, `.topbar-search` or `.icon-btn` classes: only the frame those
      rules bounded is kept (`body` canvas, `.shell`, `.main`, and the 900px
      collapse), and the topbar/nav rules with their 1500px and 1270px
      breakpoints were deleted as unconsumed. The command bar's own responsive
      behaviour is the components' business, so "port from this file" applies
      to the frame and, later, the chat dock, not to class names. Still to
      port, when the pages that use them exist: `card.css`
      (`.card`/`.grid-2`/`.grid-4`), `page.css`
      (`.page-head`/`.page-title`/`.page-sub`/`.page-actions`), `pill.css` (the
      ok/warn/danger/info/idle definitions a Badge port must match), and the
      chat dock and morph Phases 3 and 5 describe. `base.css`,
      `responsive.css` and `_main.css`'s import order are ported.
- [x] **Theme toggle** and its persistence.

## Phase 2 — Shared primitives

| Was                  | Is                                                                                                                                                                                  |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `base/pill.jsx`      | `Badge`, with five status variants (`ok`, `warn`, `danger`, `info`, `idle`) over the palette's status tokens                                                                        |
| `base/statTile.jsx`  | `base/StatTile.vue`: full `Card` composition (`CardHeader`, `CardTitle`, `CardDescription`, `CardAction`, `CardContent`), `base/Sparkline.vue`, and the `goodDirection` trend logic |
| `base/icons.jsx`     | **Not ported as a module.** Its string-keyed lookup is the pattern shadcn-vue's rules name as wrong, so icons come from `@lucide/vue` at the call site                              |
| `base/gauge.jsx`     | `base/Gauge.vue` and `base/SegmentBar.vue`, both ported. The gauge's `aria-label` announces the rounded percentage, which closes `86au`                                             |
| `base/log-row.jsx`   | `base/LogRow.vue` over `lib/log.ts` (`LOG_LEVELS`, `levelKind`, `shortTime`, `fmtValue`)                                                                                            |
| `base/format.jsx`    | `lib/format.ts` (`ago`, `compact`)                                                                                                                                                  |
| `base/task-meta.jsx` | `lib/task-meta.ts`; Vue's reactivity replaces the manual boot-time re-render the Preact module needed                                                                               |

Two decisions worth not re-litigating:

- The pill's dot did not come across to `Badge`. The Badge's own shape is the
  affordance, and the same variant now covers things that are not status pills
  (a trend indicator), where a dot reads as noise.
- The five bespoke glyphs (`dashboard`, `mappings`, `bindings`, `curators`,
  `memory`) are not drawn yet because nothing consumes them: the navigation is
  text with tooltips. If a page needs one, it becomes its own component at that
  point rather than a glyph table waiting for a caller.

- [x] **Copy verbatim.** `dashboard/activity-detail.ts`, `chat/command-scroll.ts`,
      `workflows/workflow-rows.ts`, `logs/logs-empty.ts`, and `lib/stream-state.ts`.
- [x] `base/uuid.jsx` → `src/lib/uuid.ts`.
- [x] `routing.jsx` is superseded by vue-router. Not ported.

## Phase 3 — Pages

In dependency order. Each is `git show e18fb8b2^:ui/src/<folder>/`. File counts
verified against the deletion diff.

Ported as named components, not as one file per page: see
`docs/prds/dashboard-port-brief.md#componentisation`. A page file composes and
holds nothing, shared state lives in a module beside it, and a lookup that maps
a string to a component is not written.

- [x] **Dashboard.** Health row, Throughput tiles, Token outlook, Live activity
      table, the time-of-day `greeting()`, and the click-through from an
      activity row to its task. The setup panel branches on the state machine
      rather than on "is setup present", which had shown a 100% checklist where
      the complete state belongs.
- [x] **Tasks.** List, filters, row actions, detail page with tab bar, stage
      rail, changed files, attempt config, task logs, timeline, debug view.
      Both interaction contracts survive: detail `tab`/`attempt` is written with
      `router.replace` (never a hash, and replace so a tab click adds no history
      entry and does not remount), and the list's reduced-motion-aware
      scroll-to-row reveal for deep links. `1q89`'s column priority is applied.
      One deliberate deviation: the list's inline timeline expansion is gone,
      since the row now opens `/tasks/:id` and that page owns the timeline; the
      phone layout's stacked cards are not reproduced and the table scrolls
      inside its own container instead.
- [ ] **Chat.** In progress.
- [x] **Configuration**, split into the six `/system/*` routes the approved
      design names, with the editable-vs-read-only dual mode intact: this
      composition serves a published snapshot, so the pages say they are
      read-only rather than rendering controls that cannot work.
- [x] **Logs.** Filters, live tail, pause. A 503 on the stream renders as
      "Log stream unavailable" with the reason, not as a permanent
      "reconnecting".
- [x] **Event inspector / captures.**
- [x] **Field mappings**, including the mapping editor. Its `mappingPreview`
      `capture_id` is a number: the wire type is int64 and a JSON string is
      rejected.
- [x] **Playbook bindings**, including the binding editor.
- [x] **Workflows**, keeping the "No stage data yet" state and omitting the
      Start-work form when the endpoint serves no definitions (`3tij`).
- [x] **Skills**, **Curators**, **Channels.** Capability-gated and hidden on the
      reference deployment, so all three were verified by their own empty state.

Every page was rendered against the running daemon at 1440x900 and 720px, with
no horizontal page scroll at either width.

## Phase 4 — Navigation restructure

Approved in `docs/prds/dashboard-navigation-groups.md`; build it during the
port rather than porting the flat bar first.

- [x] Five top-level items: Dashboard, Work, Agent, Events, System.
- [x] Rename under their group: Inspector, Mappings, Bindings.
- [x] Per-item hover tooltips carrying a one-line description.
- [x] Configuration splits into `/system/{status,appearance,tasks,models,repos,advanced}`.
- [x] `/settings` redirects to `/system/status` — it is in bookmarks and in the
      Go route registry.
- [x] Add Logs to the nav. It was never in it.
- [x] Preserve the `soon:` entry treatment.
- [x] The theme control moves off the topbar and onto `/system/appearance`. Two
      switches for one preference is two places to look and one of them to be
      wrong; `components/topbar/ThemeToggle.vue` is deleted, not orphaned.
- [x] Update `dashboard_tools.go` for every new route in the same change. The
      registry already carried all sixteen, so its parity test has been passing
      throughout; it is the check that a route cannot be added here and
      forgotten there.

## Phase 5 — Behaviour a naive port loses

This behaviour exists only in the deleted tree.
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
