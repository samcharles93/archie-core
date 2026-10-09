import assert from "node:assert/strict";
import test from "node:test";
import { shownActionIds } from "../src/tasks/task-actions.ts";

test("task surfaces preserve server-authorized actions and their order", () => {
  const cases: Array<[string[] | null | undefined, string[] | undefined, string[]]> = [
    [["retry", "abandon", "reject"], undefined, ["retry", "abandon", "reject"]],
    [["archive", "open_pr"], ["archive"], ["archive"]],
    [["retry", "reject"], ["archive"], []],
    [undefined, ["archive"], []],
    [null, undefined, []],
    [[], ["archive"], []],
    [["b", "a"], ["a", "b"], ["b", "a"]],
  ];
  for (const [offered, only, expected] of cases) {
    assert.deepEqual(shownActionIds(offered, only), expected, JSON.stringify({ offered, only }));
  }
});
