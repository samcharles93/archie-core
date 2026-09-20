import { test } from "node:test";
import assert from "node:assert/strict";
import { logsEmptyTitle, logsEmptyDetail } from "../src/logs/logs-empty.js";

test("filters that match nothing say so", () => {
  assert.equal(logsEmptyTitle(false, "live"), "Nothing matches");
  assert.match(logsEmptyDetail(false, "live"), /wider level/);
});

test("a deployment without durable history explains live-only", () => {
  assert.equal(logsEmptyTitle(true, "live"), "No logs yet");
  assert.match(logsEmptyDetail(true, "live"), /no durable history/i);
});

// The bug: a dead stream still promised that live logs were on their way.
test("a dead stream does not promise live logs", () => {
  assert.equal(logsEmptyTitle(true, "unavailable"), "Log stream unavailable");
  const detail = logsEmptyDetail(true, "unavailable");
  assert.ok(!/appear here while the page is open/.test(detail), `still promising live logs: ${detail}`);
  assert.match(detail, /not serving/);
});

test("a dead stream outranks the durable-history message either way", () => {
  assert.equal(logsEmptyTitle(false, "unavailable"), "Log stream unavailable");
  assert.equal(logsEmptyTitle(true, "unavailable"), "Log stream unavailable");
});

test("a reconnecting stream still reads as a filter miss, not a failure", () => {
  assert.equal(logsEmptyTitle(false, "reconnecting"), "Nothing matches");
});
