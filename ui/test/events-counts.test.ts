import assert from "node:assert/strict";
import test from "node:test";

import type { EventsCountReaders } from "../src/events/counts.ts";

const { loadEventCounts } = await import("../src/events/counts.ts");

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
