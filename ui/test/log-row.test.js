import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { logRow, levelKind, fmtValue, shortTime } from "../src/base/log-row.js";

test("levelKind maps known levels to their pill kind", () => {
  assert.equal(levelKind("ERROR"), "danger");
  assert.equal(levelKind("warn"), "warn");
  assert.equal(levelKind("Debug"), "idle");
  assert.equal(levelKind("info"), "info");
  assert.equal(levelKind(""), "info");
});

test("fmtValue stringifies objects, passes scalars through, and blanks nullish", () => {
  assert.equal(fmtValue(null), "");
  assert.equal(fmtValue(undefined), "");
  assert.equal(fmtValue(5), 5);
  assert.equal(fmtValue({ a: 1 }), JSON.stringify({ a: 1 }));
});

test("shortTime falls back to a placeholder for an unparsable time", () => {
  assert.equal(shortTime("not a date"), "--:--:--");
});

test("logRow renders every field generically, including the ones the daemon-wide feed drops", () => {
  const row = logRow({
    time: "2026-01-01T00:00:00Z",
    level: "info",
    msg: "attempt finished",
    fields: {
      stage: "tdd",
      prompt_tokens: 100,
      completion_tokens: 40,
      cached_tokens: 10,
      stop_reason: "end_turn",
      model: "sonnet-5",
    },
  });
  assert.match(row.className, /log-row/);
  assert.match(row.textContent, /attempt finished/);
  for (const key of ["stage", "prompt_tokens", "completion_tokens", "cached_tokens", "stop_reason", "model"]) {
    assert.match(row.textContent, new RegExp(key), `missing field ${key}`);
  }
});
