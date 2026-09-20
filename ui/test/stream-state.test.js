import { test } from "node:test";
import assert from "node:assert/strict";
import { streamStateFor } from "../src/base/stream-state.js";

test("a retrying connection reports reconnecting", () => {
  assert.equal(streamStateFor(0), "reconnecting");
});

test("an open connection reports live", () => {
  assert.equal(streamStateFor(1), "live");
});

// The bug: a 503 closes the stream for good, and the page claimed it was
// reconnecting forever.
test("a closed connection reports unavailable, not reconnecting", () => {
  assert.equal(streamStateFor(2), "unavailable");
});

test("an unexpected readyState is treated as unavailable rather than live", () => {
  assert.equal(streamStateFor(undefined), "unavailable");
  assert.equal(streamStateFor(99), "unavailable");
});
