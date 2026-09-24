import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const snapshot = await import("../src/lib/task-meta-snapshot.ts");

// internal/webui/testdata/task_meta.json is the served /api/task-meta payload,
// pinned byte-for-byte by TestTaskMetaPayloadMatchesFixture. Comparing the
// first-paint snapshot against it means a one-sided rename of any vocabulary
// fails on whichever side was not updated.
const fixture = JSON.parse(
  await readFile(
    new URL("../../internal/webui/testdata/task_meta.json", import.meta.url),
    "utf8",
  ),
);

// The server emits an unset optional field (needs_you, confirm) as its zero
// value; the snapshot omits it.
const dropZero = (rows: Record<string, unknown>[]) =>
  rows.map((row) =>
    Object.fromEntries(
      Object.entries(row).filter(([, v]) => v !== false && v !== ""),
    ),
  );

test("the status snapshot matches the served catalog", () => {
  assert.deepEqual(snapshot.DEFAULT_STATUSES, dropZero(fixture.statuses));
});

test("the action snapshot matches the served catalog", () => {
  assert.deepEqual(snapshot.DEFAULT_ACTIONS, dropZero(fixture.actions));
});

test("the change-status snapshot matches the served catalog", () => {
  assert.deepEqual(snapshot.DEFAULT_CHANGE_STATUSES, fixture.change_statuses);
});

test("the config schema snapshot matches the served catalog", () => {
  assert.equal(snapshot.DEFAULT_CONFIG_SCHEMA, fixture.config_schema);
});
