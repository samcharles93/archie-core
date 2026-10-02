import assert from "node:assert/strict";
import test from "node:test";

import { inForceLabel, reviewDialStatus, type ReviewDials } from "../src/settings/review.ts";
import type { ConfigView } from "../src/settings/types.ts";

const draft: ReviewDials = { precision_gate: true, approve_before_post: false };

test("reviewDialStatus reads both dials from the projection's review block", () => {
  const view = {
    review: { precision_gate: false, approve_before_post: true },
  } as ConfigView;

  const status = reviewDialStatus(view, draft);

  assert.equal(status.precision_gate.inForce, false);
  assert.equal(status.precision_gate.pending, true);
  assert.equal(status.approve_before_post.inForce, true);
  assert.equal(status.approve_before_post.pending, true);
});

test("an unavailable projection reports unknown rather than a guessed value", () => {
  const status = reviewDialStatus(null, draft);

  assert.equal(status.precision_gate.inForce, undefined);
  assert.equal(status.precision_gate.pending, false);
  assert.equal(inForceLabel(status.precision_gate.inForce), "In force: unknown");
});

test("a matching projection reports no pending change", () => {
  const view = {
    review: { precision_gate: true, approve_before_post: false },
  } as ConfigView;

  const status = reviewDialStatus(view, draft);

  assert.equal(status.precision_gate.pending, false);
  assert.equal(status.approve_before_post.pending, false);
  assert.equal(inForceLabel(status.precision_gate.inForce), "In force: on");
  assert.equal(inForceLabel(status.approve_before_post.inForce), "In force: off");
});
