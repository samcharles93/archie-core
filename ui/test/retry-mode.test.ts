import assert from "node:assert/strict";
import test from "node:test";

const { initialRetryChoice, initialRetryMode, retryChoices, retryPayload } =
  await import("../src/tasks/retry-mode.ts");

const MODES = [
  {
    id: "refresh_onto_base",
    label: "Refresh onto base",
    description: "start from base",
    default: true,
  },
  {
    id: "continue_pushed_work",
    label: "Continue pushed work",
    description: "resume the branch",
    requires_branch: true,
  },
];

test("continue is disabled when the task has no pushed branch", () => {
  const choices = retryChoices(MODES, "");
  assert.equal(choices[0].disabled, false);
  assert.equal(choices[1].disabled, true);
});

test("continue is offered when the task has a pushed branch", () => {
  const choices = retryChoices(MODES, "fix/7-thing");
  assert.equal(choices[1].disabled, false);
});

test("a whitespace branch is no branch", () => {
  assert.equal(retryChoices(MODES, "   ")[1].disabled, true);
});

test("the dialog opens on the task's persisted mode", () => {
  assert.equal(initialRetryMode(MODES, "continue_pushed_work"), "continue_pushed_work");
});

test("an unknown persisted mode falls back to the explicit server default", () => {
  assert.equal(initialRetryMode(MODES, "reset_to_head"), "refresh_onto_base");
  assert.equal(initialRetryMode(MODES, ""), "refresh_onto_base");
});

test("the payload carries the chosen mode under the wire key", () => {
  assert.deepEqual(retryPayload("continue_pushed_work"), {
    retry_mode: "continue_pushed_work",
  });
});

test("a persisted mode the task can no longer use is not preselected", () => {
  const choices = retryChoices(MODES, "");
  assert.equal(initialRetryChoice(choices, "continue_pushed_work"), "refresh_onto_base");
});

test("a persisted enabled mode is preselected", () => {
  const choices = retryChoices(MODES, "fix/7-thing");
  assert.equal(
    initialRetryChoice(choices, "continue_pushed_work"),
    "continue_pushed_work",
  );
});
