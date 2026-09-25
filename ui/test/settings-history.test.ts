import assert from "node:assert/strict";
import test from "node:test";

import { filterHistory } from "../src/settings/history.ts";

const entry = (id: number, record_key: string, actor: string, at?: string) => ({
  id, table: "resources", record_key, field: "f", old_value: 1, new_value: 2,
  version: 2, actor, source: "web", at,
});

const now = Date.parse("2026-09-25T12:00:00Z");
const entries = [
  entry(1, "channel-settings", "sam", "2026-09-25T11:00:00Z"),
  entry(2, "tool-settings", "sam", "2026-09-20T11:00:00Z"),
  entry(3, "channel-settings", "system:migration", "2026-08-01T00:00:00Z"),
  entry(4, "tool-settings", "sam"),
];

test("filterHistory keeps entries matching every filter", () => {
  const ids = (f: Parameters<typeof filterHistory>[1]) =>
    filterHistory(entries, f, now).map((e) => e.id);
  assert.deepEqual(ids({}), [1, 2, 3, 4]);
  assert.deepEqual(ids({ kinds: ["channel-settings"] }), [1, 3]);
  assert.deepEqual(ids({ actor: "sam" }), [1, 2, 4]);
  assert.deepEqual(ids({ sinceMs: 24 * 3600_000 }), [1]);
  assert.deepEqual(ids({ kinds: ["tool-settings"], sinceMs: 7 * 24 * 3600_000 }), [2]);
});

test("an entry with no time is dropped only when a time window is set", () => {
  assert.equal(filterHistory([entry(9, "k", "a")], {}, now).length, 1);
  assert.equal(filterHistory([entry(9, "k", "a")], { sinceMs: 1000 }, now).length, 0);
});
