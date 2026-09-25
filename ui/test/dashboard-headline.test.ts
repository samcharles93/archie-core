import assert from "node:assert/strict";
import test from "node:test";

import { headline } from "../src/dashboard/headline.ts";

test("the headline names what is running and what needs a human", () => {
  const cases: [number, number, { running: string; attention: string }][] = [
    [0, 0, { running: "Nothing is running", attention: "Nothing needs you" }],
    [1, 0, { running: "1 task running", attention: "Nothing needs you" }],
    [4, 1, { running: "4 tasks running", attention: "1 task needs you" }],
    [0, 3, { running: "Nothing is running", attention: "3 tasks need you" }],
  ];
  for (const [running, attention, want] of cases) {
    assert.deepEqual(headline(running, attention), want, `${running}/${attention}`);
  }
});
