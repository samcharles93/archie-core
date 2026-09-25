# Frontend UI

Authority: `.agents/skills/archie-web-ui` (stack facts, design-system rules,
tone and accessibility). This page covers what a change must reach, not how it
should look.

## Adding or changing a feature

1. The feature lives in `ui/src/<feature>/`. Move a helper to `ui/src/base/`
   only when a second feature needs it.
2. Keep wire shapes, draft state and payload building in a plain `.ts` module
   beside the components (`ui/src/bindings/binding-draft.ts` is the pattern),
   and test it in `ui/test/*.test.ts`. Components stay thin.
3. Every mutation goes through `api.*` in `ui/src/lib/api`, which sends the
   CSRF and Content-Type headers the server's mutation guard requires. A bare
   `fetch` is refused.
4. A value the server also describes (task statuses, actions, workflow inputs)
   comes from the server. Keep a local default only so the first render works,
   and keep it in step (`ui/src/lib/task-meta.ts`).
5. Run `task ui`, `task test:ui` and `cd ui && npm run typecheck`. Commit the
   regenerated `ui/dist/`; the Go binaries embed it. A fresh git worktree has
   no ignored `ui/node_modules`: run `cd ui && npm ci` before these commands.
6. **One long-lived browser connection:** `ui/src/lib/api.ts` has the only
   `new EventSource`, owned by `stores/live-updates.ts`. Feature stores consume
   topics with `live.subscribe(topic, callback)`; do not open a page-owned SSE
   stream. Logs alone opt in while mounted, replacing that same EventSource
   with `?topics=logs&since=<last cursor>`; a browser's `Last-Event-ID` only
   survives its own automatic reconnect, not a newly constructed EventSource.
   Task and log topics resume by cursor; control-plane, identity and apply-status
   topics replay the latest snapshot. Test a fetch and same-origin navigation
   after visiting Settings and Logs: the UI process serves HTTP/1.1 and six
   persistent streams can starve every later browser request.

## Adding an API route

1. The handler goes in the `internal/webui/api_<concern>.go` file for its
   concern. Check the request fully on the server; the UI's checks are only a
   convenience.
2. A mutation calls `authorizeTaskMutation` first.
3. A route whose store is not configured answers 503, and
   `internal/app/archieui/degraded_routes_test.go` pins what each route
   returns without its contract. Add the route there.
4. The dashboard runs in its own process (`archie-ui`) and must not link the
   daemon or the workflow engine (`cmd/archie-ui/architecture_test.go`). Reach
   stored data through `storecontract` interfaces wired in
   `internal/app/archieui/compose.go`.
5. Settings that live in a control-plane resource need no new route: the
   Advanced settings page edits every resource from its generated schema.
6. For a new live topic, add one process-wide producer to `internal/webui`'s
   `RunLive` and fan it through the `/api/stream` hub; `internal/app/archieui/run.go`
   starts the producers with the process context. Never create an upstream
   watch or poller in an HTTP handler. A slow client must reconnect for cursor
   and snapshot repair rather than silently lose an update. Prove the producer
   with several browser clients against the real peer, not just a scripted
   stream; `live_hub_test.go` is the example.
