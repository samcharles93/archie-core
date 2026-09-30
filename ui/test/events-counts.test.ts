import assert from "node:assert/strict";
import test from "node:test";

import type { EventsCountReaders } from "../src/events/counts.ts";

const { COUNT_SOURCES, loadEventCounts } = await import(
  "../src/events/counts.ts"
);
const { EVENTS_TABS } = await import("../src/events/tab-selection.ts");

/** Readers standing in for api.captures / api.mappings / api.bindings. */
function readers(overrides: Partial<EventsCountReaders> = {}): EventsCountReaders {
  return {
    captures: async () => ({ captures: [{}, {}] }),
    mappings: async () => ({ mappings: [{}] }),
    bindings: async () => ({ bindings: [] }),
    ...overrides,
  };
}

test("each count is the length of what its own read answered with", async () => {
  assert.deepEqual(await loadEventCounts(readers()), {
    inspector: 2,
    mappings: 1,
    bindings: 0,
  });
});

test("a load reads each list once", async () => {
  const called: string[] = [];
  const record = (name: string, response: unknown) => async () => {
    called.push(name);
    return response;
  };
  await loadEventCounts(
    readers({
      captures: record("captures", { captures: [] }),
      mappings: record("mappings", { mappings: [] }),
      bindings: record("bindings", { bindings: [] }),
    }),
  );
  assert.deepEqual(called.sort(), ["bindings", "captures", "mappings"]);
});

test("a count that could not be read stays blank, not zero", async () => {
  const counts = await loadEventCounts(
    readers({
      mappings: async () => {
        throw new Error("offline");
      },
    }),
  );
  assert.equal(counts.mappings, undefined);
  assert.equal(counts.inspector, 2, "a failed sibling read blanked its count");
  assert.equal(counts.bindings, 0);
});

test("a response without its list counts as an empty list", async () => {
  const counts = await loadEventCounts(
    readers({
      captures: async () => null,
      mappings: async () => ({}),
    }),
  );
  assert.equal(counts.inspector, 0);
  assert.equal(counts.mappings, 0);
});

test("every tab of the chain has one source, and the strip reads them in order", () => {
  assert.deepEqual(
    COUNT_SOURCES.map((source) => source.tab),
    EVENTS_TABS.map((tab) => tab.id),
    "a tab whose count nothing reads shows a chip that can never fill",
  );
});
