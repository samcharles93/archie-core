# Task run detail as a master-detail split pane

**Status:** Draft
**Authority:** the panel contracts stay as ratified in
`docs/prds/task-run-detail.md`; this document changes only the run page's
arrangement of them.
**Compounds with:** `docs/prds/task-run-detail.md` (attempt attribution, the
log stage filter, the per-panel honesty rules).

## Decision

The run page's five tabs become two panes. The stage rail is the **master**,
always visible on the left. The four context panels — log, changed files,
configuration, debug — are the **inspector** on the right, and their tab bar
moves into the inspector. The "Stages" tab stops existing: the rail is not a
view of the page, it is the page's spine.

Selecting a stage in the rail is a command, not a binding: it switches the
inspector to the log tab and sets the log pane's stage filter to that stage.
Changing the filter afterwards is the operator's choice and is not overridden.
Clearing the selection returns the filter to "all stages". Changed files,
configuration and debug stay attempt-scoped; a stage selection never pretends
to scope them.

## What does not change

- No new endpoint, RPC, table or configuration field. Every read is one the
  page already makes.
- The URL contract: `tab` and `attempt` ride in the query string and every
  view survives a reload. `tab=stages` stays a valid URL and renders the
  inspector with the log tab; the page never generates it again.
- Attempt attribution, the log stage filter's coverage limit, and every
  honesty rule in `task-run-detail.md`. Each pane states what it cannot show
  under the same rendering rules that document defines.
- The attempt selector and the task header stay pinned above both panes.

## Arrangement

- Left pane: fixed-width rail (~320px) with the attempt's stage rows. Status,
  duration, error and per-stage events render in the rail; the rail scrolls
  independently.
- Right pane: the inspector's tab bar plus the selected panel, filling the
  remaining width.
- Below 900px (the shell's existing frame breakpoint) the rail stacks above
  the inspector; neither pane is hidden.
- Keyboard: the rail keeps its list semantics; the inspector keeps the
  WAI-ARIA tabs pattern the tab bar already provides.

## Acceptance

1. The rail is visible without opening any tab, and no "Stages" trigger exists.
2. Clicking a stage opens the log filtered to that stage; clearing the
   selection shows all stages.
3. `tab=stages` loads and renders the log tab.
4. Every inspector tab and attempt deep-link survives a reload.
5. Below 900px the rail stacks above the inspector.
6. `task check` passes clean and `ui/dist` matches the sources.