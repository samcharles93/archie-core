import assert from "node:assert/strict";
import test from "node:test";

const { shownActionIds } = await import("../src/tasks/task-actions.ts");

test("a surface renders every control the server offers when it does not narrow", () => {
  assert.deepEqual(shownActionIds(["retry", "abandon", "reject"]), [
    "retry",
    "abandon",
    "reject",
  ]);
});

test("a surface that narrows renders only the control it named", () => {
  assert.deepEqual(shownActionIds(["archive", "open_pr"], ["archive"]), [
    "archive",
  ]);
});

test("a narrowed surface never invents a control the server did not offer", () => {
  assert.deepEqual(
    shownActionIds(["retry", "reject"], ["archive"]),
    [],
    "the head offered Archive for a task the server does not let it archive",
  );
});

test("a task with no action list offers nothing", () => {
  assert.deepEqual(shownActionIds(undefined, ["archive"]), []);
  assert.deepEqual(shownActionIds(null), []);
  assert.deepEqual(shownActionIds([], ["archive"]), []);
});

test("the server's order is kept, never the surface's", () => {
  assert.deepEqual(shownActionIds(["b", "a"], ["a", "b"]), ["b", "a"]);
});
