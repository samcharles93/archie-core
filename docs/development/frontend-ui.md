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

## Testing the dashboard

Authority: the runner itself is `ui/package.json`'s `test` script: `node --test
--experimental-strip-types test/*.test.ts` over `ui/test/*.test.ts`. It runs
pure TypeScript modules with `node`'s type stripping — no SFC loader, no DOM,
no second dependency beyond what the app already carries. That is the standard,
and a decision record for when it is deliberately not enough:

- **Test archie's logic in a module; leave SFC wiring alone.** Wire shapes,
  draft state, copy conditions, filters and payload building live in plain
  `.ts` files beside the components and are tested in `ui/test/`. Components
  stay thin, so there is normally nothing in an SFC that is archie logic.
- **Installed primitives are upstream-tested, not re-tested here.**
  `reka-ui` (2.10.5 here) maintains colocated tests per component in its own
  repo (Vitest/jsdom + axe: Switch, TagsInput, NumberField, Select, the
  arrow-navigation composables). The npm package ships none of those files, so
  this coverage is inherited through the version bound, not the tarball —
  `grep -l test node_modules/reka-ui` finds nothing. Duplicating them as local
  mount tests would test a dependency.
- **A `.vue` import cannot run under the module runner.** `node --test` fails
  such a module with `ERR_UNKNOWN_FILE_EXTENSION: .vue` and the runner has no
  built-in loader for SFCs. A file under `ui/test/` that imports a `.vue` is
  therefore a broken test, not a weak one.
- **If a DOM mount harness is ever needed** (a page-level accessibility
  contract, cross-component interaction, focus behaviour): `happy-dom` +
  `@vue/test-utils` + `vitest` scoped to a separate DOM test glob, keeping the
  module runner for everything else. Measured on the ui tree (vitest 5.0.2,
  happy-dom 20.14.5, @vue/test-utils 2.5.1): they add 24 top-level
  `node_modules` entries and 27 MB on a 296 MB install, a working 2-test mount
  suite (role=radiogroup vs role=switch, aria-label, click activation against
  the real SFCs) runs in 1.2 s on top of the current 0.5 s module suite, and
  `vue-tsc` (7.4 s) dominates `task test:ui` either way. The single-runner
  alternative (absorbing the existing suite into vitest) was measured
  infeasible as-is: all 29 existing files import `node:test`, which vitest
  cannot collect ("No test suite found in file" for every one), so it would be
  a rewrite of the whole suite, and its runtime was not measured. Nothing in
  `ui/dist` or CI changes either way; happy-dom is pure JS, no browser binary.
- **What the module standard does not verify:** whether a call site passes an
  `aria-label` or which `v-if` branch shows. Those live in SFC templates and
  are only visible with a mount. If a page's accessible-name wiring is the
  risk, say so in the page's task and reach for the harness decision above
  rather than quietly growing mount tests. Never suppress a lint rule or a
  test to keep SFC behaviour out of the gate.

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
