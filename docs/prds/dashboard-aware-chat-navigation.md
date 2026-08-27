# Dashboard-aware chat: Archie sees the page, and points the operator there

**Status:** Design decision (before implementation)
**Date:** 2026-08-26
**Compound:** the "chat available on any page" surface work (global chat presence)

## Decision

Make the web chat **dashboard-aware**: the agent is told which dashboard page
the operator is on *right now*, knows what each page is for, and can **point**
the operator to the relevant page with a **clickable destination chip** the UI
renders and follows - Option B (context + navigate), not just "tell me in text."

Three seams, each an existing extension point, one new tool and one message field:

1. **Current page, into the prompt.** The browser sends its route with every
   chat message; the gateway threads it into the system prompt's runtime-metadata
   env block. Archie always knows where the operator is looking.
2. **Page index.** A page_index tool lists the dashboard pages and what each one
   shows, so Archie knows what to point at - the agent "has the map".
3. **Navigate.** A dashboard_navigate tool returns a structured path/label; the web
   UI renders it as a clickable chip that routes the operator there. This is the
   "plenty of resources" the requirement named.

We deliberately keep the agent's answer honest: the chip is *evidence* it can
validly produce because dashboard_navigate is a real tool whose result is
rendered, not a markdown link the model invents.

## Why now

This is the natural next step of the "chat on any page" work, and the original
request named it explicitly: *"archie should be able to see what page you're on
too. and point out where to go. so it needs plenty of resources."* Today the chat
is a full-page route with no knowledge of the rest of the dashboard; the agent
cannot see which page the operator is on and cannot reliably direct them anywhere.
The critique of the chat UI already identified "chat is not available on any page"
as the largest structural gap (P1). This design is the capability that makes that
gap worth closing.

## Shape of the API

### Message field (gateway.go)

Add Page to Message:

```go
// Page is the dashboard route the operator is on when they sent this message
// (e.g. "/tasks", "/logs"). Empty for non-web channels, which have no page
// context. It is per-message, not per-session: the operator navigates freely
// while a conversation stays open.
Page string
```

### Web request (api_chat.go)

chatMessageRequest gains Page, decoded into Message.Page in decodeChatMessage.

### System prompt config (prompt.go)

SystemPromptConfig gains Page; BuildSystemPrompt passes it through to the template.
The env block of archie.md.tpl renders the current page when set.

### Page index (dashboard_tools.go, new)

A package-level registry mapping route to {Label, Description}. page_index returns
the list so the model knows what each page shows:

```go
type DashboardPage struct {
	Path        string // "/tasks"
	Label       string // "Tasks"
	Description string // "Issues Archie picked up, and where they stand."
}
```

### Navigation tool (dashboard_tools.go)

dashboard_navigate validates its path against the registry and returns the resolved
path/label. Unknown path means a clear error, not a guess. The model must call the
tool (not invent a link) to produce a chip, so the chip is honest.

### Tool wiring (turn.go)

extraTools already merges TaskTools + SessionTools. Add the dashboard tools alongside
them, gated on the channel being the web UI. The tool list is built from the same
registry the prompt's page line comes from, so the agent never points at a page it
was not told about.

### Web UI (chat-render.js, chat-tools.js)

A dashboard_navigate tool call renders as a **clickable chip**: an anchor with
href="#/tasks" and the label. Because the app routes on location.hash, the chip is a
real link that works from any page once the chat is a global presence. The chip is
styled as an action, not a code line, so it visibly invites a click.

### Route transfer (chat.js)

Each sendMessage includes the current location.hash route as page in the stream
request body, so the backend gets the operator's live position.

## What we deliberately do NOT do

- **No navigation from a markdown link.** The model must use dashboard_navigate; a
  bare "click here" is not rendered as a trusted route.
- **No full-page route hijack from the chat.** The chip sets location.hash.
- **No page index for non-web channels.** Telegram/discord have no dashboard.
- **No automatic history annotation.** Page is per-message from the browser.
- **No change to the dangerous-action approval flow.** Separately tracked.

## Acceptance criteria

1. Chat messages from any page carry the current page to the agent's prompt.
2. page_index returns every dashboard page with a plain-language description.
3. dashboard_navigate renders a clickable chip that routes to the target page.
4. dashboard_navigate rejects an unknown route with a clear error, no chip.
5. Non-web chat channels get neither tool, and Message.Page stays empty.
6. Tests cover prompt page line; navigate resolve/reject; non-web exclusion; and the
   web request decoder round-trips page.

## Files this change touches

- internal/gateway/gateway.go - Message.Page
- internal/gateway/prompt.go + templates/archie.md.tpl - SystemPromptConfig.Page, env line
- internal/gateway/dashboard_tools.go (new) - registry, page_index, dashboard_navigate
- internal/gateway/turn.go - merge dashboard tools into extraTools
- internal/gateway/prompt_test.go + dashboard_tools_test.go (new) - prompt + tool tests
- internal/webui/api_chat.go - chatMessageRequest.Page, decoder
- ui/src/chat/chat.js - send page with each stream request; render navigate chips
- ui/src/chat/chat-render.js + chat-tools.js + chat.css - clickable chip
- ui/src/main.js + shell - persistent chat presence + current-page tracking (separate)

> The persistent "chat on any page" surface (drawer/launcher in the shell) is the
> companion change that makes the chip navigable from anywhere. It is the same
> capability and should land with or just before it, but is not required for the
> backend to be correct and testable on its own.
