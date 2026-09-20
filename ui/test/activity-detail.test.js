import { test } from "node:test";
import assert from "node:assert/strict";
import { activityDetail } from "../src/dashboard/activity-detail.js";

// Live activity renders whatever the daemon put in an event's detail. An
// agent_finish payload is a few thousand characters of transcript, and printed
// raw it turned one table row into six viewports (archie-core-qo7f). The cell
// shows a single scannable line; the full text stays reachable, never silently
// dropped.

test("a short detail is shown as-is and needs no expansion", () => {
  assert.deepEqual(activityDetail({ detail: "session-memory" }), {
    text: "session-memory",
    full: "session-memory",
    truncated: false,
  });
});

test("a long detail is cut to one line and keeps the full text", () => {
  const long = "x".repeat(5000);
  const result = activityDetail({ detail: long });
  assert.equal(result.truncated, true);
  assert.ok(result.text.length < 200, `cell text is still ${result.text.length} chars`);
  assert.equal(result.full, long, "full payload was dropped rather than kept");
});

test("a multi-line detail collapses to its first meaningful line", () => {
  const result = activityDetail({
    detail: "stage fix: agent parked (gate_parked):\n[golangci-lint] run ./...\nlevel=warning msg=deprecated",
  });
  assert.equal(result.text, "stage fix: agent parked (gate_parked):");
  assert.equal(result.truncated, true);
});

test("leading blank lines do not produce an empty cell", () => {
  assert.equal(activityDetail({ detail: "\n\n  real content\nmore" }).text, "real content");
});

test("message is used when detail is absent, and absence is not an error", () => {
  assert.equal(activityDetail({ message: "fallback" }).text, "fallback");
  assert.deepEqual(activityDetail({}), { text: "", full: "", truncated: false });
  assert.deepEqual(activityDetail(undefined), { text: "", full: "", truncated: false });
});
