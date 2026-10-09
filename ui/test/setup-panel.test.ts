import assert from "node:assert/strict";
import test from "node:test";
import { setupPanelState, type Setup } from "../src/dashboard/setup-preference.ts";

test("the setup panel shows only unfinished steps", () => {
  const cases: Array<[boolean[] | null, string, number]> = [
    [null, "omit", 0],
    [[], "omit", 0],
    [[true, true], "omit", 0],
    [[true, false, false], "incomplete", 2],
  ];
  for (const [done, kind, remaining] of cases) {
    const setup: Setup | null = done === null ? null : { steps: done.map((done, i) => ({ title: `step ${i}`, done })) };
    const state = setupPanelState(setup);
    assert.equal(state.kind, kind, JSON.stringify(done));
    assert.equal(state.remaining.length, remaining, JSON.stringify(done));
    assert.ok(state.remaining.every((step) => !step.done));
  }
});
