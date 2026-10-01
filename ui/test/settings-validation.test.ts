import assert from "node:assert/strict";
import test from "node:test";

import {
  collectIssues,
  draftValidators,
  validateModelRoles,
  validateSchedules,
  validateSchedulingPolicy,
  type DraftValidator,
} from "../src/settings/validation.ts";

test("scheduling policy refuses a non-positive interval and accepts a positive one", () => {
  assert.deepEqual(
    validateSchedulingPolicy({
      poll_interval: "0s",
      label: "archie",
      dispatch: { trigger: "assignee" },
    }),
    [{ path: "poll_interval", label: "Poll interval", message: "Must be longer than zero." }],
  );
  assert.deepEqual(
    validateSchedulingPolicy({
      poll_interval: "1m0s",
      label: "archie",
      dispatch: { trigger: "assignee" },
    }),
    [],
  );
});

test("scheduling policy requires a label only for a label-matching trigger", () => {
  const missing = validateSchedulingPolicy({
    poll_interval: "30s",
    label: "",
    dispatch: { trigger: "either" },
  });
  assert.deepEqual(missing, [
    { path: "label", label: "Label", message: "Required when the trigger is label or either." },
  ]);
  assert.deepEqual(
    validateSchedulingPolicy({
      poll_interval: "30s",
      label: "",
      dispatch: { trigger: "assignee" },
    }),
    [],
  );
});

test("schedules refuse an empty id and a duplicate, keeping each row's path", () => {
  const issues = validateSchedules([
    { id: "daily" },
    { id: "" },
    { id: "daily" },
  ]);
  assert.deepEqual(issues, [
    { path: "1.id", label: "Schedule 2", message: "ID is required." },
    { path: "2.id", label: "Schedule 3", message: 'Duplicate schedule "daily".' },
  ]);
  assert.deepEqual(validateSchedules([{ id: "daily" }, { id: "weekly" }]), []);
});

test("model roles require provider/model", () => {
  assert.deepEqual(validateModelRoles({ builder: "gpt-4o" }), [
    { path: "builder", label: "builder", message: "Use provider/model." },
  ]);
  assert.deepEqual(validateModelRoles({ builder: "openai/gpt-4o", planner: "" }), []);
});

test("collectIssues runs only dirty sections' validators and stamps the kind", () => {
  const validators: Record<string, DraftValidator | undefined> = {
    a: () => [{ path: "x", label: "X", message: "bad" }],
    b: () => [{ path: "y", label: "Y", message: "also bad" }],
  };
  const values: Record<string, unknown> = { a: { x: 1 }, b: { y: 2 } };
  const issues = collectIssues(["a", "b"], (kind) => values[kind], validators);
  assert.deepEqual(issues, [
    { path: "x", label: "X", message: "bad", kind: "a" },
    { path: "y", label: "Y", message: "also bad", kind: "b" },
  ]);
  // A dirty section with no validator contributes nothing; a clean section is
  // never asked.
  assert.deepEqual(collectIssues(["a"], (kind) => values[kind], {}), []);
});

test("a dirty scheduling draft is blocked through the real validator map", () => {
  const issues = collectIssues(
    ["scheduling-policy"],
    () => ({ poll_interval: "0s", label: "archie", dispatch: { trigger: "assignee" } }),
    draftValidators,
  );
  assert.deepEqual(issues, [
    {
      path: "poll_interval",
      label: "Poll interval",
      message: "Must be longer than zero.",
      kind: "scheduling-policy",
    },
  ]);
});
