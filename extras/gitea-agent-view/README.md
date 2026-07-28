# Gitea agent-run view

Untracked reference copies — deploy by hand onto the gitea container.

Renders inside Gitea's own Actions page (real `base/head` / `repo/header`
/ `base/footer`, real `.Repository`/`.CurWorkflow` context) via the
`body_inner_post.tmpl` hook — no full-file template override needed.
Confirmed 2026-07-22 that this hook receives the same per-page context
as `templates/repo/actions/list.tmpl`.

## Files → destinations

| file | destination on gitea container | role |
| --- | --- | --- |
| `extra_tabs.tmpl` | `$GITEA_CUSTOM/templates/custom/extra_tabs.tmpl` | adds the "Agent Run" repo tab |
| `body_inner_post.tmpl` | `$GITEA_CUSTOM/templates/custom/body_inner_post.tmpl` | loads the CDN CSS/JS + agent-monitor.js, gated on the sentinel |
| `agent-monitor.js` | `$GITEA_CUSTOM/public/assets/agent-monitor.js` | swaps the runs-list segment's content for the terminal, opens the SSE connection |

`$GITEA_CUSTOM` is usually `/data/gitea` in the container — confirm via
Site Administration > Configuration, or `gitea help`.

Restart Gitea after copying either file (custom/templates is only read
at startup unless `RUN_MODE = dev`).

## How it fits together

`extra_tabs.tmpl` links to the real `/actions` route with a sentinel
`?workflow=__agent_monitor__`. Gitea's Actions handler happily sets
`.CurWorkflow` to that string (it doesn't validate it matches a real
workflow file) and renders the stock page as normal — sidebar, run-count
bar, "0 workflow runs" panel, all real Gitea markup. `body_inner_post.tmpl`
then fires on that same page, sees the sentinel, and loads
`agent-monitor.js`, which:

1. finds the real `.ui.attached.segment` inside `.flex-container-main`
   — the element list.tmpl already sizes correctly to hold the runs
   list — and swaps its content for the terminal, rather than hiding or
   duplicating anything
2. hides the run-count/filter bar directly above that segment (nothing
   to filter once the terminal's showing)
3. styles the segment as a floating rounded card (border radius, shadow)
4. mounts the xterm.js terminal inside it and opens the SSE connection

The sidebar is deliberately left alone — it's real, always-current Gitea
markup (actual workflow files, actual routes). An earlier version tried
hiding it and hardcoding a lookalike sidebar instead; that goes stale the
moment a workflow is added/renamed, so it was dropped in favor of this.
Nesting the terminal inside other stock wrapper classes (e.g.
`.ui.top.attached.header`, meant for the slim counter bar) also produced
a tiny clipped box in testing — reason `.ui.attached.segment` specifically
is the target now.

**Why the terminal logic is a separate `.js` file, not inline in the
template:** Gitea's CSP requires a per-request nonce for inline
`<script>` blocks, and that nonce isn't exposed to custom/templates.
External `<script src>` tags match the `*` already in `script-src` and
load with zero CSP changes — discovered the hard way when the first
inline-script version got silently blocked by the browser.

No stock Gitea file is copied or overridden, so there's nothing to
re-diff on version bumps — this is the whole reason the hook route won
out over overriding `list.tmpl`.

## Before it'll actually connect

`body_inner_post.tmpl` reads `internal/webui`'s `GET /events` SSE
stream. Two things need doing on the archie-core side first:

1. **Reachability** — the browser loads Gitea's origin, so it needs a
   path to the webui SSE endpoint. Either reverse-proxy `/events` under
   Gitea's own host (cleanest — avoids CORS entirely), or add CORS
   headers to `handleSSE` in `internal/webui/webui.go` for Gitea's
   origin, and set `?webui=` on the tab link (or edit `WEBUI_BASE` in
   the template) to point at it.
2. **Auth** — `internal/webui`'s own doc comment says "it has no auth" —
   this whole path is trusted-network only right now, same as the
   dashboard. Don't expose it publicly without adding one.

`/events` has no server-side repo/task filter; `agent-monitor.js`
filters client-side on `event.repo`, so it still receives every task's
events over the wire — fine at current volume, revisit if that stream
gets busy.

Note: the `[cors]` section in Gitea's own `app.ini` does NOT help here —
it controls CORS headers Gitea sends to let *other* origins call Gitea's
API, not the reverse (the browser, loaded from Gitea's origin, fetching
archie-core's separate `internal/webui` server). The CORS header needs
to live on that webui response, or use the reverse-proxy route instead.
