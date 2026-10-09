import assert from "node:assert/strict";
import test from "node:test";
import { activeTab, availableTabs } from "../src/events/tab-selection.ts";

test("Events hides only capabilities explicitly disabled by the server", () => {
  const cases: Array<[Record<string, boolean> | null | undefined, string[]]> = [
    [null, ["inspector", "mappings", "bindings"]],
    [undefined, ["inspector", "mappings", "bindings"]],
    [{ captures: true }, ["inspector", "mappings", "bindings"]],
    [{ captures: true, mappings: true, bindings: false }, ["inspector", "mappings"]],
    [{ captures: false, mappings: false, bindings: false }, []],
  ];
  for (const [sections, expected] of cases) {
    assert.deepEqual(availableTabs(sections).map((tab) => tab.id), expected, JSON.stringify(sections));
  }
});

test("Events bookmarks select an available tab or fall back to the first", () => {
  const cases: Array<[Record<string, boolean> | null, string, string]> = [
    [null, "mappings", "mappings"],
    [{ captures: false, mappings: false, bindings: true }, "inspector", "bindings"],
    [{ captures: false, mappings: false, bindings: true }, "", "bindings"],
    [{ captures: false, mappings: false, bindings: false }, "bindings", ""],
  ];
  for (const [sections, requested, expected] of cases) {
    assert.equal(activeTab(requested, availableTabs(sections)), expected, `${JSON.stringify(sections)}: ${requested}`);
  }
});
