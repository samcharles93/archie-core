# Task run detail as a master-detail split pane

**Status:** Approved
**Authority:** the panel contracts stay as ratified in
`docs/prds/task-run-detail.md`; this document changes only the run page's
arrangement of them.
**Compounds with:** `docs/prds/task-run-detail.md` (attempt attribution, the
log stage filter, the per-panel honesty rules).

## Decision

The run page's five tabs become two panes. The stage rail is the **master**,
always visible on the left. The four context panels — log, changed files,
configuration, debug — are the **inspector** on the right, and their tab bar
moves into the inspector. The "Stages" tab stops existing.

Selecting a stage switches the inspector to the log tab and sets the log
pane's stage filter to that stage. Changing the filter afterwards is the
operator's choice and is not overridden; rail selection never touches the
level filter. Clicking the selected stage again returns the filter to all
stages. A stage with no name is rendered but not selectable.

Switching attempts resets the stage filter to all stages: a selection was
commanded on the attempt it was made on, and a different attempt presents a
different stage list. Changed files, configuration and debug stay
attempt-scoped; a stage selection never scopes them.

## What does not change

- No new endpoint, RPC, table or configuration field. Every read is one the
  page already makes.
- The URL contract: `tab` and `attempt` ride in the query string and survive
  a reload. Log filters are not part of the URL; a reload returns the log to
  all stages. `tab=stages` stays a valid URL and renders the log tab; the
  page never generates it again.
- Attempt attribution, the log stage filter's coverage limit and every
  honesty rule in `task-run-detail.md` still apply: each pane states what it
  cannot show under that document's rendering rules.
- The task header and the attempt selector sit above both panes.

## Arrangement

- Left pane: fixed-width rail (20rem) with the attempt's stage rows. Status,
  duration, error, the stage's agent report, the attempt's status badge and
  start time, and the attempt-attribution footnotes render in the rail.
- Right pane: the inspector's tab bar plus the selected panel, filling the
  remaining width.
- Below the shell's 900px frame breakpoint the rail stacks above the
  inspector; neither pane is hidden.
- Keyboard: the rail keeps its list semantics and each stage row is a real
  button, so selection and deselection work without a pointer; the inspector
  keeps the WAI-ARIA tabs pattern the tab bar already provides.

## Acceptance

1. The rail is visible without opening any tab, and no "Stages" trigger exists.
2. Clicking a stage opens the log filtered to that stage; clicking it again
   shows all stages.
3. After a stage selection, a filter change made by the operator is kept, and
   the level filter survives the selection.
4. Switching attempts resets the stage filter to all stages, and no rail row
   on the new attempt renders as selected unless the operator selected it
   there.
5. `tab=stages` loads and renders the log tab.
6. Every inspector tab and attempt deep-link survives a reload.
7. A stage is selectable and deselectable by keyboard, and the selection
   mirrors the log pane's stage filter.
8. Below 900px the rail stacks above the inspector.
9. `task check` passes clean and `ui/dist` matches the sources.