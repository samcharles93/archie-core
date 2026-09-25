# Prompt for Claude Code

Unzip this bundle into the repo at `docs/design/ui-redesign/`, then paste the prompt below.

---

The design handoff for the Archie UI redesign is in `docs/design/ui-redesign/`. It covers a new theme, a new
top-level navigation, the Settings pages (Channels moves in there), and the Dashboard, Task detail, Workflows,
New task dialog and Events pages.

Read `README.md` in full, then look at every PNG in `screenshots/`. The `00-current-*.png` files show how the
pages look today; `P0-navigation-map.png` shows what moves where. The static HTML in `mockups/` has the exact
CSS if you need it. `tokens/tokens.css` holds the colours, fonts and sizes.

Treat the spec as a hypothesis to check against the code, not a spec to copy exactly.

Before changing anything:
1. Find how the UI is built: framework, router, component library, where styles and theme colours live,
   and where the purple comes from.
2. Map today's routes and the Work / System menus to the proposed navigation in README §2. List every
   page that isn't covered and where you'd put it.
3. For Settings and Channels: find how each section loads and saves config, how "Restart required",
   "Version", "History" and the audit log work, and where "Connecting" comes from. Map every field in
   README §5 to its real config key and type.
4. Answer the backend questions in README §5.2 and §9.
5. Check what data the Dashboard, Task detail and Workflows pages already get, and list anything the
   mockups show that isn't available.

Then write a short migration plan using the PR sequence in README §8. Adjust it to what you found and
list anything the mockups assume that the backend doesn't support. Stop and show me the plan before you
write code.

When I approve, do PR 1 only (theme tokens across the whole app, no layout changes) on a new branch, with
before/after screenshots of each page.
