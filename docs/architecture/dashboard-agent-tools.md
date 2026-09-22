# The dashboard's tools for an in-browser agent

The dashboard registers in-browser tools an agent can call through the page's model
context. Four are read-only — `list_tasks`, `get_task`, `recent_events`,
`daemon_health` — and three recover stuck work: `retry_task`, `stop_task`,
`cancel_task`. Each wraps the same `ui/src/lib/api.ts` client the dashboard itself
calls, so an agent and a person exercise one path rather than two.

The four judgement verbs are withheld for the reason `identity.md` gives: an agent
that can approve can approve its own work.

## What a tool call can be attributed to

A page can invoke its own tools, so an agent's call and a page script's call reach
the server identically — the difference does not exist on the wire, and no header
recovers it. A tool call is therefore attributed to the signed-in identity the
request presented, with its source recorded as a hint; it never records that an
agent acted, and it never records that a human approved. An agent's action is
attributable only on a surface where the agent presents its own credential.

## Confirmation is a signal, not a control

The three recovery tools require an explicit `confirm: true`, which makes a
consequential call deliberate rather than incidental. It is not a security control:
an agent can trivially satisfy it, and the browser cannot enforce
`consequentialHint` at all — Chrome accepts it at registration and omits it from
`getTools()`, so no client can see what the browser does not surface.

The boundary that holds is the session. These tools cannot exceed what the operator
signed in at the provider is already permitted to do.

## Two ways the surface fails silently

- **A secure context is required.** On a plain-HTTP origin `document.modelContext` is
  absent even with the flag enabled, and nothing reports why. `mkcert` covers local
  development.
- **The dashboard embeds `ui/dist` into its binary.** Rebuilding the distribution is
  not deploying it: a running process can serve a bundle without tools while the
  built one has them, or serve a bundle older than its own binary.
