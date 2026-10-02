import assert from "node:assert/strict";
import test from "node:test";

const { EVENTS_TABS, activeTab, availableTabs, stepsFor } =
  await import("../src/events/tab-selection.ts");

test("every tab stays when the composition reports nothing", () => {
  assert.deepEqual(
    availableTabs(null).map((t) => t.id),
    ["inspector", "mappings", "bindings"],
  );
  assert.deepEqual(
    availableTabs(undefined).map((t) => t.id),
    ["inspector", "mappings", "bindings"],
  );
});

test("a tab is offered only when its own capability is backed", () => {
  const ids = availableTabs({
    captures: true,
    bindings: false,
    mappings: true,
  }).map((t) => t.id);
  assert.deepEqual(
    ids,
    ["inspector", "mappings"],
    "a capability the server refuses still shows its tab",
  );
});

test("a section the server never mentioned stays visible", () => {
  const ids = availableTabs({ captures: true }).map((t) => t.id);
  assert.deepEqual(
    ids,
    ["inspector", "mappings", "bindings"],
    "an unreported section hid its tab",
  );
});

test("the requested tab wins when it is available", () => {
  const available = availableTabs({
    captures: true,
    bindings: true,
    mappings: true,
  });
  assert.equal(activeTab("mappings", available), "mappings");
});

test("a tab this composition cannot back falls back to the first it can", () => {
  const available = availableTabs({
    captures: false,
    bindings: true,
    mappings: false,
  });
  assert.equal(
    activeTab("inspector", available),
    "bindings",
    "a bookmark from a fuller deployment showed nothing",
  );
  assert.equal(
    activeTab("", available),
    "bindings",
    "no tab named did not land on the first available one",
  );
});

test("nothing backed reports no tab rather than guessing one", () => {
  const available = availableTabs({
    captures: false,
    bindings: false,
    mappings: false,
  });
  assert.equal(available.length, 0);
  assert.equal(activeTab("bindings", available), "");
});

test("the tabs are the three Events surfaces, in reading order", () => {
  assert.deepEqual(
    EVENTS_TABS.map(({ id, section }) => [id, section]),
    [
      ["inspector", "captures"],
      ["mappings", "mappings"],
      ["bindings", "bindings"],
    ],
  );
});

test("a step carries the strip's own word, not the tab's", () => {
  const steps = stepsFor(availableTabs(null));
  assert.deepEqual(
    steps.map(({ id, label }) => [id, label]),
    [
      ["inspector", "Capture"],
      ["mappings", "Map"],
      ["bindings", "Bind"],
    ],
    "the strip named a panel the way the URL does",
  );
  assert.equal(steps[0]?.hint, "webhooks received");
});

test("the strip offers exactly the steps this composition can back", () => {
  const steps = stepsFor(
    availableTabs({ captures: true, bindings: true, mappings: false }),
  );
  assert.deepEqual(
    steps.map((s) => s.id),
    ["inspector", "bindings"],
    "the strip showed a step whose panel is not backed, so it went nowhere",
  );
});

test("no backed capability is an empty strip, not three dead steps", () => {
  assert.deepEqual(
    stepsFor(availableTabs({ captures: false, mappings: false, bindings: false })),
    [],
  );
});
