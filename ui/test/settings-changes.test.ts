import assert from "node:assert/strict";
import test from "node:test";

import { diffValues, saveInOrder } from "../src/settings/changes.ts";

test("diffValues lists each changed leaf with its path", () => {
  const before = {
    poll_interval: "30s",
    max_retries: 3,
    dispatch: { label: "archie", reaction: "eyes" },
    profiles: ["a"],
    servers: [{ name: "x", enabled: true }],
  };
  const after = {
    poll_interval: "1m0s",
    max_retries: 3,
    dispatch: { label: "archie", reaction: "rocket", extra: true },
    profiles: ["a", "b"],
    servers: [{ name: "x", enabled: false }],
  };
  assert.deepEqual(diffValues(before, after), [
    { path: "poll_interval", before: "30s", after: "1m0s" },
    { path: "dispatch.reaction", before: "eyes", after: "rocket" },
    { path: "dispatch.extra", before: undefined, after: true },
    { path: "profiles", before: ["a"], after: ["a", "b"] },
    { path: "servers.0.enabled", before: true, after: false },
  ]);
});

test("diffValues reports nothing for equal values and a removed row as one change", () => {
  assert.deepEqual(diffValues({ a: [1, 2] }, { a: [1, 2] }), []);
  assert.deepEqual(
    diffValues({ rows: [{ n: 1 }, { n: 2 }] }, { rows: [{ n: 1 }] }),
    [{ path: "rows.1", before: { n: 2 }, after: undefined }],
  );
});

test("saveInOrder saves one at a time and stops at the first failure", async () => {
  const calls: string[] = [];
  const outcome = await saveInOrder(["a", "b", "c"], async (kind) => {
    calls.push(kind);
    return kind !== "b";
  });
  assert.deepEqual(calls, ["a", "b"]);
  assert.deepEqual(outcome, { saved: ["a"], failed: "b", untouched: ["c"] });
});

test("saveInOrder reports every kind saved when none fails", async () => {
  assert.deepEqual(
    await saveInOrder(["a", "b"], async () => true),
    { saved: ["a", "b"], failed: undefined, untouched: [] },
  );
});
