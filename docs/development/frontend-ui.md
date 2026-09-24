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
   regenerated `ui/dist/`; the Go binaries embed it.

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
