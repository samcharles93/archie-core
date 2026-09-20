# Dashboard navigation groups + Configuration split -- decision

**Status:** Approved for implementation
**Date:** 2026-09-20
**Beads issue:** `archie-core-ocfm`
**Related:** `archie-core-gl96` (Logs absent from the nav -- subsumed here),
`archie-core-ggqo` (Configuration's page width -- moot once it splits)

## Problem

`ui/src/main.jsx` declares eleven navigable routes in one flat bar. Three of
them (Skills, Curators, Channels) are capability-gated and hidden on the
reference deployment, so the eight items normally visible are the best case,
not the worst: a composition that serves every section wraps the bar. Logs is
declared but was never given a nav entry at all, reachable only from a
dashboard link.

The labels compound it. "Event inspector", "Field mappings" and "Playbook
bindings" are three long labels for three faces of one subject, and they sit
beside "Dashboard" and "Tasks" as though they were peers of equal weight.

Configuration is the other half. One route renders roughly ten sections --
update status, work lifecycle, identity, budgets, storage, listen address,
repositories, models and providers, configuration sources, plus the operator
cards rehomed in `archie-core-tf20` -- in a 4021px page with no in-page
navigation. There is no way to send someone to Repositories.

## Decision

Five top-level nav items. Dashboard is a link; the rest are dropdowns.

| Group | Items |
|---|---|
| Dashboard | (link, `/`) |
| Work | Tasks, Workflows |
| Agent | Skills, Curators |
| Events | Inspector, Mappings, Bindings |
| System | Status, Logs, Appearance, Task settings, Models & providers, Repositories, Channels, Advanced |

Items are renamed because the group now carries the context: "Event inspector"
becomes "Inspector" under Events, and so on. A group renders only if at least
one of its children survives capability gating, and a group left with one
child still renders as a dropdown -- a bar whose shape changes per deployment
is harder to learn than one with a predictable skeleton.

### Route map

Configuration dissolves into real routes. Anchors into one long page were
rejected: they leave the 4021px page intact and a deep link lands the reader
mid-scroll with no context above it.

| Was | Becomes | Content |
|---|---|---|
| `/settings` | `/system/status` | Update status, configuration sources, daemon listen address |
| -- | `/system/appearance` | Theme, and the density/motion preferences that follow it |
| `/settings` | `/system/tasks` | Work lifecycle: statuses, operator actions, budgets |
| `/settings` | `/system/models` | Model roles, providers |
| `/settings` | `/system/repos` | Repositories and their per-repo gate overrides |
| `/settings` | `/system/advanced` | Identity, storage and sandboxing, dangerous actions |
| `/captures` | `/captures` | unchanged; label becomes "Inspector" |
| `/mappings` | `/mappings` | unchanged; label becomes "Mappings" |
| `/bindings` | `/bindings` | unchanged; label becomes "Bindings" |

`/settings` is kept as a permanent redirect to `/system/status`. It is the URL
in existing bookmarks, in `dashboardPages` (`internal/gateway/dashboard_tools.go`,
which the chat agent navigates by), and in prior conversation history; breaking
it to save one route entry is not a trade worth making.

`dashboardPages` gains the new routes in the same change. That registry is
guarded by `TestDashboardPagesRegistryCoversEveryRoute`, so a route added here
and forgotten there fails the build -- which is how the deleted `/chat` entry
was caught.

### Dropdown behaviour

Each item carries a one-line description rendered beneath its label, not a
hover tooltip. A dropdown has the width for it, the description is then visible
to everyone rather than only to a mouse user who waits, and it survives on
touch where hover does not. The `title` attribute is not used for this.

The menu is shadcn-vue's `DropdownMenu` (Reka UI underneath), which already
supplies the roving focus, ArrowDown/ArrowUp movement, Escape-closes-and-
restores-focus, and outside-pointerdown behaviour this would otherwise hand-
roll. It opens on click, never on hover -- hover-opened menus on a control
plane are a misclick away from a destructive page. The trigger keeps
`aria-current` styling when any of its children is the active route, so the bar
still says where you are.

This section was written against the Preact dashboard; the decisions above
survive the Vue migration unchanged, only the primitive supplying them
changed.

### Explicitly not in scope

- No change to what any page renders, beyond which route renders it. The
  Configuration split moves sections between files; it does not redesign them.
- `/system/appearance` ships with the theme control moved out of the topbar
  icon and nothing else. The density and motion preferences named above are the
  reason the page exists as a page rather than a section, not part of this
  change.
- Mobile keeps the existing scrollable row of top-level items; groups expand
  in place rather than opening a floating menu. Nested floating menus on a
  phone are a separate problem.

## Consequences

The bar goes from eleven items to five, and stops depending on capability
gating to fit. Logs gets a home. Configuration becomes six linkable pages, and
`archie-core-ggqo`'s question about its page width answers itself, because no
single page is long enough for the cap to matter.

The cost is a real routing change with redirects to maintain, and a nav that
now needs keyboard and screen-reader behaviour it did not need as a row of
links. Both are paid once.
