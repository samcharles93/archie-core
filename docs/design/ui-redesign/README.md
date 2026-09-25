# Archie UI Redesign — Handoff

A redesign of the Archie web UI:

- a calmer **"Graphite" theme** that replaces the purple
- a **new navigation**: five top-level places, with all configuration moved under Settings
- new layouts for **Settings** (every section, plus Channels moved in), the **Dashboard**,
  **Task detail**, **Workflows**, a **New task** dialog and **Events**

This folder is self-contained. Read this file first, then look at the screenshots.

```
README.md              ← this spec (read it all before planning)
CLAUDE_PROMPT.md       ← the prompt to paste into Claude Code
tokens/tokens.css      ← theme as CSS custom properties (source of truth)
tokens/tokens.json     ← same values as JSON (for Tailwind / JS themes)
screenshots/
  00-current-*.png     ← how each page looks TODAY
  P0-navigation-map    ← today → proposed navigation, and what moves where
  P1…P5                ← app pages: dashboard, task detail, workflows, new task, events
  S1…S5                ← settings: sidebar + rows, personas, tools & MCP, review drawer, channels
  T-theme-components   ← palette, type, buttons, pills, toggles, inputs
mockups/*.html         ← static HTML of each screenshot; open in a browser, inspect for exact CSS
source/*.dc.html       ← original design-canvas sources (reference only, needs a runtime that isn't included)
```

> **How to treat this.** The mockups are a target, not a pixel spec. They are static HTML
> written by hand; the app's framework, component library, router and data model win. Where the
> mockups assume something the backend doesn't have, flag it rather than faking it.
> Text in `[square brackets]` in a mockup is a placeholder for real data.

---

## 1. Problems being fixed (all pages)

1. **Purple everywhere.** Purple-tinted backgrounds, borders, buttons, checkboxes, bars and links all compete for attention.
2. **Repeated chrome.** Every settings card has its own "Connecting" pill, "Restart required" pill, "Version 1",
   "Save changes" and "History". Workflows and Channels repeat the pattern.
3. **Raw config shapes leak through.** Durations as Go strings (`1h0m0s`, `0s`), bytes as `2000000`,
   maps as key/value boxes, secret refs as two separate Engine/Key text fields, booleans as bare checkboxes with no explanation.
4. **No descriptions.** Almost no field says what it does.
5. **Things live in the wrong place.** Channels (configuration) sits under Work; "Start work" sits inside the
   workflow editor page; the Channels audit table only covers Channels; workflow analytics mix every workflow together.
6. **The most important information isn't first.** The Dashboard opens with quality gates; the "3 need you" count is a small button.

## 2. Navigation (screenshot `P0-navigation-map.png`)

**Top bar (every page):** logo · **Dashboard · Tasks · Workflows · Events · Settings** · spacer ·
"Jump to…" search (⌘K) · **New task** button · connection pill · account avatar.
The Work and System dropdowns go away. The floating chat button stays, restyled (neutral fill, accent icon).

| Place | Job | Contains |
|---|---|---|
| Dashboard | What needs me right now | Needs-you queue, live activity, health (quality gates, tokens) |
| Tasks | The work | Task list (filter by state), task detail |
| Workflows | How work runs | List with runs/success/cost, definition editor, Performance and Runs tabs |
| Events | What comes in | Inspector, Mappings, Bindings |
| Settings | Everything you configure | **Agents:** Personas, Schedules, Scheduling policy · **Integrations:** Channels, Tools & MCP, Plugins · **Runtime:** Container runtime · **History** |

What moves:

| What | Moves to | Why |
|---|---|---|
| Start work card (Workflows page) | **New task** dialog from the top bar and ⌘K | Starting work isn't editing a workflow |
| Channels (Work menu) | Settings › Integrations › Channels | It's configuration; gets the shared setting rows and save flow |
| Audit table (Channels page) | Settings › History, filterable by section | One change log for all settings; replaces per-card History links |
| Per workflow table | Columns in the Workflows list | Stats next to the workflow you're about to open |
| Slowest stages · Most failures | Workflow › Performance tab | Scoped to the selected workflow |
| Connecting / Version pills on every card | One connection status in the top bar | Connection is global |
| Settings "Managed elsewhere" row | Removed; those are the Events tabs | They already live there |
| Task "Attempt 2 is not recorded" column | Attempts list in the task side rail | Shows every attempt and its outcome |

The dropdown contents of today's Work and System menus were inferred from screenshots. **Check the router** and
place any page not covered here using the same rule: configure → Settings, act on → where the work is.

## 3. Theme

All values are in `tokens/tokens.css`. Rules that matter more than the hex values:

- **One accent, used sparingly.** Default sage `#86b5a0`. Only for: active nav/list item (2px inset left bar + raised background),
  active tab underline, focus borders/rings, "edited" dots and counts, toggle "on" track, primary button fill, success text,
  the "live"/"running"/"connected" dot, and positive bars. Never for backgrounds, headings or borders at rest.
- Keep the accent a **single variable** so it can be swapped (sage / steel `#8aa4c2` / sand `#d4a373` / muted violet `#a397c9`).
- **Neutral, slightly warm greys**, no hue tint on surfaces and no background gradients. Hierarchy comes from surface steps and borders.
- **Amber = needs attention** (parked, awaiting reply, applies after restart, connecting). **Red = failed / destructive / risky**
  (errors, delete, unrestricted filesystem). No other hues except the agent state swatches in the tokens.
- **Fonts:** IBM Plex Sans for UI; **IBM Plex Mono for every machine value**: config values, IDs, keys, env var names,
  image refs, durations, paths, event names, task numbers, log lines, counts in tables.
- Text contrast: `--text-3` is the dimmest text allowed, and only for hints and captions.

## 4. Components (screenshot `T-theme-components.png`)

Build these once and reuse everywhere. Heights are 36px unless noted.

| Component | Spec |
|---|---|
| **Button** | primary (accent fill, `--text-on-accent`), secondary (`--raised` + `--border-button`), ghost (transparent, `--text-2`), destructive (red text, transparent). 13px/500, radius 6. Small variant 30px/12px. **One primary per view.** |
| **Text input** | `--bg` fill, `--border-input`, radius 6, mono 13px. Focus: accent border + 3px `--accent-ring`. Invalid: red border + 12px red message. |
| **Input with unit** | input + attached suffix (`MB`, `sec`, `chars`) or unit `<select>`. |
| **Duration input** | number + unit select. Convert to/from Go durations (`30s`, `1m0s`, `72h0m0s`) only at the API boundary. |
| **Byte size input** | number + `MB`; store bytes (2,000,000 bytes = 2 MB, decimal). |
| **Stepper** | − / value / + for small integers. |
| **Toggle** | 36×20 track. On = accent (or red for risky settings), off = `--border-input`. Always labelled; risky ones sit in a bordered row with a one-line consequence. Real checkbox / `role="switch"` underneath. |
| **Segmented control** | 2–4 exclusive options, radiogroup with arrow keys. Selected segment `--raised-2`. |
| **Tabs** | 44px, 13px, `--text-3`; active = `--text` + 2px accent underline. Left-aligned, sized to content (not stretched full width). Optional mono count chip. |
| **Secret reference** | one row: lock icon · engine (mono, dim) · `/` · key (mono) · "Change". Status line below: accent ✓ "Credential resolved by Archie" or red "Credential not found". Replaces separate Engine/Key inputs and the "Credential configured" checkbox. |
| **Chip input** | for lists of IDs (Telegram allowed users): mono chips with × plus an inline input. |
| **Status pill** | radius 999, 12px. Neutral with coloured dot (connection, running, listening), warn (parked, awaiting reply, applies after restart), accent outline (edited count, default), danger (invalid). |
| **Card** | `--surface`, `--border-card`, radius 10. Header row: optional 32px icon tile · title (15px/500) + hint · right-side status/action. |
| **Stat strip** | one card split into equal cells by 1px dividers: label (12px dim), value (30px/600), sub (12px dim). |
| **Data table** | uppercase 11px headers, 13px rows, 1px `--border-subtle` dividers, mono for machine values, long text truncates with ellipsis. |
| **Progress bar** | 4–6px track `--raised-2`, fill accent (good) or red (failing). |
| **Empty state** | icon tile + title + one-line hint + the action (a button, or a copyable value). |
| **Alert banner** | amber or red tinted box: icon · title · mono detail · one action on the right. |
| **Nav item** (sidebars) | 34–36px, radius 6, 13px `--text-2`. Active: `--raised` + 2px inset accent bar. |
| **Dialog** | 640px, `--surface-2`, radius 12, header (title + hint + close), body, footer (shortcut hint · Cancel · primary). Dim backdrop. |

Icons: 2px-stroke line icons. Use the app's existing icon set if it has one (e.g. Lucide).

## 5. Settings

### 5.1 Settings shell (`S1-settings-sidebar-setting-rows*.png`)
- **Left sidebar (248–264px)** with grouped nav: AGENTS (Personas, Schedules, Scheduling policy), INTEGRATIONS (Channels, Tools & MCP, Plugins),
  RUNTIME (Container runtime), divider, History. Optional mono counts (Personas `6`). A section with unsaved edits shows an accent dot.
- **Content column** max ~920–980px. Section header: 24px title, 14px description, amber "Applies after restart" pill when relevant.
- **Groups** inside a section: 13px uppercase eyebrow.
- **Setting row:** 2-column grid `260–280px | 1fr`, gap 32px, 16–20px vertical padding, 1px top border.
  Left: label (13px/500) + hint (12px dim). Right: the control.
- Sidebar items are links (route or hash per section).

### 5.2 Save model (`S1` save bar + `S4-settings-review-drawer.png`)
Replace every per-card Save button with one page-level flow:
1. Track edits per field against the loaded config. Edited inputs get an accent border and a "was `old`" hint.
2. When anything is dirty, a **sticky save bar** appears: accent dot · "N unsaved change(s)" · field names · **Discard** · primary
   **"Save & restart"** if any dirty field needs a restart, otherwise **"Save"**.
3. The primary button opens the **Review drawer** (480px, right, dim backdrop): "Review N changes", "Saved together as config version N+1",
   amber restart callout (optionally with running agent count), changes grouped by section as cards with **Revert** and a mono diff
   (red `− old`, green `+ new`), footer Discard all · Save only · Save & restart.
4. Personas keep their own save footer in the editor panel but still feed the page dirty state.

**Backend questions to answer first:** can several sections be saved atomically (or will saves be sequential with partial-failure handling)?
Is "restart required" known per field or per section? Is there a restart action? If not, "Save & restart" becomes "Save" plus a
persistent "Restart pending" banner.

### 5.3 Personas (`S2-settings-personas.png`)
- Left: filter + New; list rows = name + one-line prompt; default persona has an accent-outline **default** pill; selected row = raised + accent bar.
- Right: editor. Header: name + default pill · Duplicate · Delete. Body: Name (mono) and **Default persona** toggle; **System prompt** as a large
  mono textarea with "N chars · ~N tokens". Footer: "Saved · version N · History" · Discard · Save persona.
- The old separate "Default" field becomes the per-persona toggle (setting one unsets the others).
- The "try this persona" preview row is optional/future.
- The mockup uses **top tabs** for section navigation; that's the narrow-width fallback. Use the sidebar shell on desktop.

### 5.4 Schedules
Not mocked. Card empty state ("No schedules yet" + "Add schedule"); populated = list rows like Personas.

### 5.5 Scheduling policy (`S1`)
- DISPATCH: *Trigger* segmented Assignee / Label (show the Label input only when Label is chosen); *Ack reaction* mono input.
- STATE LABELS: bordered table `STATE | LABEL | ⌫` with coloured state swatch, mono label input, trash button, "+ Add state".
  Today's UI shows display name ("Dead"), key (`dead`) and value (`agent:dead`); the mockup merges name and key.
  If the key must stay editable, keep a small mono input in the State column.
- RETRIES & POLLING: *Max retries* stepper; *Poll interval* duration input.

### 5.6 Channels (moved from Work; `S5-settings-channels.png`, today `00-current-channels.png`)
- One card per channel. **Configured** channels are expanded with setting rows; **unconfigured** ones collapse to a single row
  (icon · name · one-line purpose · "Not set up" pill · **Set up** button that expands the fields).
- **Telegram** (running): header status pill "Running" (accent). Rows: *Bot token* = secret reference (`env / HEYARCHIE_TELEGRAM_BOT_TOKEN`) + resolved line;
  *Allowed users* = chip input of numeric IDs, hint "Everyone else is ignored."
- **Email** (collapsed): expands to *Listen address*, *Relay address*.
- **Webhook** (collapsed): expands to *Deliver to*, *Path*, *Template*, *Signing secret* (secret reference), and *Webhook address*
  (today's top-level "Webhook addr" belongs here if it only serves the webhook channel; confirm).
- **CHAT SESSION DEFAULTS · ALL CHANNELS** card with setting rows: *Workspace* (mono path), *Filesystem access* (Unrestricted filesystem toggle in a
  **red** bordered row: "On: agents can read and write anywhere this user can."), *Show tool calls* toggle, *Max steps*
  ("0 = no limit": **confirm the real semantics**), *Rate limit* as "[n] requests per [n] [unit]" ("Off while either value is 0": confirm),
  *Models*, *Operator*.
- Footer line: "Version N · N fields set by `actor` N ago · View in History". The audit table itself moves to Settings › History.
- Channel status (running / stopped / error) must come from the backend, not from whether it's configured.

### 5.7 Tools & MCP (`S3-settings-tools-mcp.png`)
- Header action **Add MCP server**. MCP servers card (full width): empty state offering "stdio command" / "HTTP / SSE endpoint"
  (only the transports the backend supports); populated rows = name, transport, enabled toggle, edit/delete.
- **MiniMax** card: header "Enabled" toggle; API key as secret reference (`bws / MINIMAX_API_KEY`) + resolved line.
- **Web fetch** card: "Enabled" toggle; *Max download* (bytes as MB); *Timeout* (duration, sec); *Allow private networks* as a bordered
  toggle row. Disable the fields when the card is off.
- **Tool output policy** card: *Max result length* (`chars`, hint "About N tokens per tool call", ~4 chars/token), *Spill directory*.

### 5.8 Plugins
Not mocked. Setting rows for *Module dir*, *Plugin dir*, *Secret engine dir*, *Skills dir* (mono, placeholder = effective default if exposed).

### 5.9 Container runtime (left side of `S4`)
*Image* (full width, mono), *Max concurrency* (stepper), *Max uptime* (duration), *Network* (mono),
*Pull policy* (select: If missing / Always / Never → `missing` / `always` / `never`), *Volume TTL* (duration), *Profiles* (mono).

### 5.10 History (new, not mocked)
The Channels audit table generalised to all settings: When · Section · Field (mono) · Old → New (mono, truncated, expandable) ·
Version · Changed by. Filters: section, actor, date. Every "History" link elsewhere deep-links here with the section filter applied.

## 6. App pages

### 6.1 Dashboard (`P1-dashboard.png`, today `00-current-dashboard.png`)
- Header: "Good morning" + a one-line summary that **names the action** ("Nothing is running. 3 tasks need you." with the count in amber). Logs button on the right.
- **Stat strip**: Working now / Needs you (amber value) / Delivered / Tokens used, with the same subtitles as today.
- Two columns: main (1fr) and right rail (380px).
- Main, first: **Needs you** card (count pill). One row per task: state pill (Parked / Awaiting reply) · title (link) ·
  mono meta line (`repo #issue · workflow · reason or last message`, truncated) · **Open** (ghost) + the next action
  (**Start new run** for parked, **Reply** for awaiting). "All tasks" link in the header.
- Main, second: **Live activity** table: Event (mono, small colour square by kind, `×N` chip when consecutive identical events are grouped) · Task (mono) ·
  Detail (truncated) · When. Header: live pill + segmented filter All / Tasks / System.
- Right rail: **Quality gates** (big pass rate, one row per gate with `passed / runs` and a bar: red when failing, accent when passing) and
  **Token outlook** (Today, "Next 30 days at this rate", a 7-day mini bar chart, and a note when history is shorter than a week).
  Today's full-width purple bar that shows nothing is removed.

### 6.2 Task detail (`P2-task-detail.png`, today `00-current-task-detail.png`)
- Breadcrumb `Tasks / #1`, title, meta row: status pill · workflow (mono) · `repo #issue` link. Actions: **Archive** (ghost), **Start a new run** (primary).
- **Alert banner** for parked/failed tasks: plain-language title ("Parked: couldn't prepare the worktree"), mono error detail, "Jump to log line".
- Two columns: main card with tabs **Log · Changed files (count) · Configuration · Debug**; right rail (320px).
- Log tab: toolbar = level select, stage select, search, Download. Log lines in mono: time · level (ERROR in red) · message with tag chips
  (`component=daemon`, `task=1`). Error lines get a faint red background and red left border. Footer "End of attempt N · N lines".
- Right rail: **Attempts** list (dot colour = outcome; selected attempt highlighted; unrecorded attempts shown dimmed) and
  **Details** list (Status, Workflow link, Repository, Issue ↗, plus Persona, Tokens, Started if available).

### 6.3 Workflows (`P3-workflows.png`, today `00-current-workflows.png`)
- Header: title + description ("Success rate is merged tasks over all runs"), **Restore shipped** (ghost), **New workflow**.
- Left (420px): list = name + id (mono) · runs · success (`n of m` + bar) · avg tokens (compact: 4.7M, 282k). Selected = raised + accent bar.
- Right: editor card. Header: name + origin pill (e.g. "shipped · edited"; derive from the real origin field) + run count.
  Tabs **Definition · Performance · Runs**.
  - Definition: *ID* input; *Steps* read-only preview (mono step chips joined by arrows, parsed from YAML);
    *YAML* editor with line numbers, mono, light syntax colouring, live "✓ Valid" / error indicator.
    Footer: "Version N · History" · Remove (destructive ghost) · Restore this workflow · **Validate & save**.
  - Performance (not mocked): today's **Slowest stages** and **Most failures**, filtered to this workflow.
  - Runs (not mocked): tasks that used this workflow, linking to Task detail.
- The **Start work** card leaves this page (see 6.4).

### 6.4 New task dialog (`P4-new-task-dialog.png`)
Replaces the Start work card. Opened from the top bar button, ⌘K, and optionally from a workflow ("Start task with this workflow").
- Fields: *Repository* (mono, hint with recent repos), *Workflow* as a 3-column card radiogroup showing each workflow's runs and merge rate,
  *Title*, *Instructions* (textarea), and **Identity** tucked into an "Advanced · run as identity" disclosure.
- Footer: "⌘↵ to start" · Cancel · **Start task**. On success, go to the new task's detail page.

### 6.5 Events (`P5-events.png`, today `00-current-events.png`)
- Header with description ("Webhooks come in, get mapped to event types, and bindings turn them into work.") and a **Listening** pill.
- Left-aligned tabs **Inspector · Mappings · Bindings** with count chips. (Reordered to follow the flow; keep the old order if you prefer.)
- Inspector: a 3-step strip **Capture → Map → Bind** with counts; then **Recent captures** (empty state with the copyable endpoint
  `/webhooks/capture/<source>` and a Copy button) and **Event types** ("From payload" action, empty state explaining proposals).

## 7. Behaviour and accessibility

- Real `<button>`, `<a href>`, `<input>`, `<select>`, `<label for>`; icon-only buttons get `aria-label`. Toggles use checkbox/`role="switch"`.
  Segmented controls and workflow cards are radiogroups with arrow-key support. Dialogs trap focus and close on Esc.
- Visible focus everywhere (accent ring). Touch targets ≥36px (44px on mobile).
- Validate on blur; invalid fields block Save and are listed in the save bar.
- Unsaved-changes guard on route leave.
- Skeleton rows while loading; the top-bar connection pill reflects the socket/API state (Connecting amber, Connected accent, Disconnected red).
- Live tables (activity, captures) insert new rows without layout jump; group consecutive identical events.
- Narrow widths (<960px): settings sidebar → top tabs; two-column layouts stack; drawers and dialogs go full-width.

## 8. Suggested PR sequence

1. **Theme tokens only.** Swap purple for Graphite across the whole app. No layout changes. *Done when no purple remains.*
2. **Shared components** from §4.
3. **Top bar + navigation** (§2): new top-level items, New task button (can open the existing Start work form at first), one connection pill, route moves/redirects.
4. **Settings shell** (sidebar groups, setting rows) and move Channels into it. Sections still save the old way.
5. **Settings sections**: Scheduling policy, Plugins, Container runtime, Tools & MCP, Channels, Personas.
6. **Unified save bar + review drawer + Settings › History** (needs the §5.2 backend decisions). Remove per-card save buttons and pills.
7. **Dashboard.**
8. **Task detail.**
9. **Workflows** (list + editor + Performance tab) and **New task dialog**; remove the Start work card.
10. **Events.**

Each PR: before/after screenshots and a note of anything deferred or unsupported.

## 9. Open questions / out of scope

- Real meaning of `max_steps = 0` and a zero rate limit (the mockup assumes "no limit" / "off").
- Whether "Webhook addr" is webhook-channel-only or global.
- Where the Dashboard's Needs-you reasons and last agent message come from.
- Persona preview, light theme, user-facing accent picker (nice to have).
- Atomic multi-section save and "restart now" (see §5.2).
