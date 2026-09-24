import assert from "node:assert/strict";
import test from "node:test";

const { setupPanelState } =
  await import("../src/dashboard/setup-preference.ts");

import type { Setup } from "../src/dashboard/setup-preference.ts";

function setupWith(...done: boolean[]): Setup {
  return { steps: done.map((d, i) => ({ title: `step ${i}`, done: d })) };
}
type Setup = ReturnType<typeof setupWith>;

// The completed setup renders no panel: a configured daemon is the absence of
// a problem, not a card announcing it. The state machine only distinguishes
// "nothing to show" from "there is work left".
test("no setup steps omits the panel", () => {
  assert.equal(setupPanelState(null).kind, "omit");
  assert.equal(setupPanelState(setupWith()).kind, "omit");
});

test("remaining steps render the checklist", () => {
  const state = setupPanelState(setupWith(true, false, false));
  assert.equal(state.kind, "incomplete");
  assert.equal(state.remaining.length, 2);
});

// The old contract celebrated completion with a "Setup complete" card the
// operator had to dismiss. The celebration is gone: all steps done is omit.
test("a fully completed setup omits the panel", () => {
  assert.equal(setupPanelState(setupWith(true, true)).kind, "omit");
});

// If setup later becomes incomplete again, the checklist returns regardless
// of anything the operator did while it was complete.
test("the checklist returns when setup regresses", () => {
  const state = setupPanelState(setupWith(true, false));
  assert.equal(state.kind, "incomplete");
  assert.equal(state.remaining.length, 1);
});
