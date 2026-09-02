import { test } from "node:test";
import assert from "node:assert/strict";
import { describeTimelineEvent } from "../src/tasks/timeline-event.js";

test("stage events name the stage, workflow, duration, and failure", () => {
  assert.deepEqual(
    describeTimelineEvent({
      kind: "stage_finish",
      workflow: "implement",
      stage: "baseline",
      data: { duration_ms: 1250, error: "task check failed" },
    }),
    {
      title: "Stage failed: baseline",
      detail: "implement workflow · ran for 1.3 s — task check failed",
    },
  );
});

test("retry events explain which failure caused the retry", () => {
  assert.deepEqual(
    describeTimelineEvent({
      kind: "task_retried",
      detail: "retried via the dashboard",
      data: { retry_count: 2, previous_stage: "baseline", previous_reason: "task check failed" },
    }),
    { title: "Retry 2", detail: "after baseline: task check failed" },
  );
});

test("agent events expose stage efficiency and cache usage as fresh vs cached", () => {
  // prompt_tokens (280,000) INCLUDES the 240,000 cache hits -- showing it
  // next to "cached" reads as two separate charges when only 40,000 of it
  // was billed at full price. The fresh figure is the disclosed remainder.
  assert.deepEqual(
    describeTimelineEvent({
      kind: "agent_finish",
      stage: "analyse",
      data: {
        status: "passed",
        iterations: 7,
        tokens: 282066,
        prompt_tokens: 280000,
        completion_tokens: 2066,
        cached_tokens: 240000,
        model: "openai/gpt-5.6-luna",
      },
    }),
    {
      title: "Agent passed: analyse",
      detail:
        "7 iterations · 282,066 tokens · 40,000 fresh prompt · 2,066 completion · 240,000 cached · openai/gpt-5.6-luna",
    },
  );
});

test("agent events with zero-valued usage fields omit them entirely", () => {
  // Pre-v0.1.24 persisted events lack the breakdown fields, and a
  // failed run still publishes agent_finish with the zero Result. Either
  // shape must not render "0 prompt · 0 completion · 0 cached".
  const result = describeTimelineEvent({
    kind: "agent_finish",
    stage: "baseline-fix",
    data: {
      status: "",
      iterations: 0,
      tokens: 0,
      prompt_tokens: 0,
      completion_tokens: 0,
      cached_tokens: 0,
      model: "openai/gpt-5.6-luna",
    },
  });
  assert.equal(result.title, "Agent finished: baseline-fix");
  assert.equal(result.detail.includes("0 "), false, `detail should not mention zero counts: ${result.detail}`);
  assert.equal(result.detail, "openai/gpt-5.6-luna");
});

test("stage durations render minutes and hours, not raw seconds", () => {
  const r1 = describeTimelineEvent({ kind: "stage_finish", stage: "analyse", data: { duration_ms: 8_500 } });
  assert.equal(r1.detail, "ran for 8.5 s");
  const r2 = describeTimelineEvent({ kind: "stage_finish", stage: "analyse", data: { duration_ms: 40_600 } });
  assert.equal(r2.detail, "ran for 41 s");
  const r3 = describeTimelineEvent({ kind: "stage_finish", stage: "fix", data: { duration_ms: 5 * 60_000 + 12_000 } });
  assert.equal(r3.detail, "ran for 5 min 12 s");
  const r4 = describeTimelineEvent({ kind: "stage_finish", stage: "tdd", data: { duration_ms: 1 * 3_600_000 } });
  assert.equal(r4.detail, "ran for 1 h");
  const r5 = describeTimelineEvent({ kind: "stage_finish", stage: "analyse", data: { duration_ms: 90_000 } });
  assert.equal(r5.detail, "ran for 1 min 30 s");
  const r6 = describeTimelineEvent({ kind: "stage_finish", stage: "analyse", data: { duration_ms: 3_599_600 } });
  assert.equal(r6.detail, "ran for 1 h");
});

test("cancelled stages are described as interrupted rather than failed", () => {
  assert.deepEqual(
    describeTimelineEvent({
      kind: "stage_finish",
      stage: "implement",
      data: { error: "context canceled", interrupted: true },
    }),
    { title: "Stage interrupted: implement", detail: "context canceled" },
  );
});

test("unknown event kinds become readable labels", () => {
  assert.deepEqual(describeTimelineEvent({ kind: "human_approved", detail: "approved via dashboard" }), {
    title: "Human Approved",
    detail: "approved via dashboard",
  });
});
