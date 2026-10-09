import assert from "node:assert/strict";
import test from "node:test";

const { initialRetryChoice, initialRetryMode, retryChoices } =
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

test("retry modes requiring a branch are disabled without one", () => {
  for (const [branch, disabled] of [["", true], ["   ", true], ["fix/7-thing", false]] as const) {
    const choices = retryChoices(MODES, branch);
    assert.equal(choices[0].disabled, false, branch);
    assert.equal(choices[1].disabled, disabled, branch);
  }
});

test("retry selection preserves usable persisted modes and otherwise falls back", () => {
  for (const [persisted, expected] of [
    ["continue_pushed_work", "continue_pushed_work"],
    ["reset_to_head", "refresh_onto_base"],
    ["", "refresh_onto_base"],
  ]) {
    assert.equal(initialRetryMode(MODES, persisted), expected, persisted);
  }
  for (const [branch, persisted, expected] of [
    ["", "continue_pushed_work", "refresh_onto_base"],
    ["fix/7-thing", "continue_pushed_work", "continue_pushed_work"],
  ]) {
    assert.equal(initialRetryChoice(retryChoices(MODES, branch), persisted), expected, branch);
  }
});
