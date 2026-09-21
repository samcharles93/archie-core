import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

/**
 * The Events surfaces are one page with three tabs, and the navigation entry is
 * always live (Sam's call): the tabs behind it prune themselves per capability,
 * so an operator on a composition that can only back some of them still reaches
 * the ones it can. What must not come back is three sibling destinations.
 */
const router = await readFile(new URL("../src/router/index.ts", import.meta.url), "utf8");
const nav = await readFile(new URL("../src/lib/nav.ts", import.meta.url), "utf8");
const page = await readFile(new URL("../src/events/EventsPage.vue", import.meta.url), "utf8");
const registry = await readFile(new URL("../../internal/gateway/dashboard_tools.go", import.meta.url), "utf8");

test("the Events page is the tab host, and the old paths only redirect", () => {
  assert.match(page, /Tabs/, "the page composes tabs");
  assert.match(page, /CapturesPage/, "the Inspector tab is the captures surface");
  assert.match(page, /BindingsPage/, "the Bindings tab is the bindings surface");
  assert.match(page, /MappingsPage/, "the Mappings tab is the mappings surface");

  // Each former destination survives as a bookmark, never as a nav entry: the
  // registry test parses this table and skips exactly the lines marked so.
  for (const path of ["/captures", "/mappings", "/bindings"]) {
    const line = router.split("\n").find((l) => l.includes(`path: "${path}"`));
    assert.ok(line, `${path} is no longer routed at all, so its bookmarks break`);
    assert.match(line, /nav: false/, `${path} is still a navigation entry`);
    assert.match(line, /redirect/, `${path} does not redirect to the tab that replaced it`);
  }
});

test("Events is one live navigation entry, not a group of three", () => {
  const entry = nav.split("\n").find((l) => l.includes('"/events"'));
  assert.ok(entry, "the navigation model does not reference /events");
  assert.doesNotMatch(nav, /label:\s*"Events",\s*paths:/, "Events is still a three-item group");
});

test("the page registry and the route table still agree", () => {
  assert.match(registry, /Path: "\/events"/, "the agent's page registry does not know /events");
  for (const path of ["/captures", "/mappings", "/bindings"]) {
    assert.doesNotMatch(registry, new RegExp(`Path: "${path}"`), `${path} is still a registered page`);
  }
});
