import { test } from "node:test";
import assert from "node:assert/strict";
import { render, cleanup } from "@testing-library/preact";
import { StageRail } from "../src/tasks/stage-rail.jsx";

// The frozen shape of GET /api/tasks/{id}/attempts. Attempt 1 failed on
// `implement`; attempt 2 is still running. The attempt-level duration_ms is
// present on purpose: the rail must not render it.
const ATTEMPTS = {
  task_id: 42,
  current_attempt: 2,
  unattributed_events: 3,
  attempts: [
    {
      attempt: 1,
      status: "failed",
      started_at: "2026-09-18T07:00:00.000Z",
      finished_at: "2026-09-18T07:04:00.000Z",
      duration_ms: 1_000_000,
      event_count: 14,
      stages: [
        { name: "prepare", seq: 0, status: "ok", started_at: "2026-09-18T07:00:00.123Z", duration_ms: 1200, error: "" },
        {
          name: "implement",
          seq: 1,
          status: "failed",
          started_at: "2026-09-18T07:00:01.323Z",
          duration_ms: 230000,
          error: "stage implement: builder exited 1",
        },
      ],
    },
    {
      attempt: 2,
      status: "running",
      started_at: "2026-09-18T07:05:00.000Z",
      event_count: 2,
      stages: [{ name: "analyse", seq: 0, status: "running", started_at: "2026-09-18T07:05:00.000Z" }],
    },
  ],
};

function mount(props) {
  const { container, unmount } = render(<StageRail {...props} />);
  return { root: container, unmount };
}

function stageRow(root, name) {
  return [...root.querySelectorAll(".run-stage")].find(
    (row) => row.querySelector(".run-stage-name")?.textContent === name,
  );
}

test("each stage renders its name, duration and terminal status", () => {
  const { root, unmount } = mount({ state: ATTEMPTS, attemptNumber: 1, events: [] });
  const prepare = stageRow(root, "prepare");
  assert.ok(prepare, "the prepare stage should render");
  assert.match(prepare.textContent, /prepare/);
  assert.match(prepare.textContent, /ok/);
  assert.match(prepare.textContent, /ran for 1\.2 s/);
  const implement = stageRow(root, "implement");
  assert.match(implement.textContent, /ran for 3 min 50 s/);
  assert.match(implement.textContent, /stage implement: builder exited 1/);
  unmount();
});

// W2: merging attempts by stage name is the failure this whole feature exists to
// prevent.
test("the rail shows only the selected attempt's stages", () => {
  const { root, unmount } = mount({ state: ATTEMPTS, attemptNumber: 1, events: [] });
  assert.ok(stageRow(root, "prepare"));
  assert.ok(stageRow(root, "implement"));
  assert.equal(stageRow(root, "analyse"), undefined, "attempt 2's stages must not appear in attempt 1's rail");
  assert.match(root.textContent, /Attempt 1/);
  unmount();
});

test("an interrupted stage is not labelled failed", () => {
  const state = {
    ...ATTEMPTS,
    attempts: [
      {
        attempt: 1,
        status: "interrupted",
        started_at: "2026-09-18T07:00:00.000Z",
        stages: [{ name: "implement", seq: 0, status: "interrupted", duration_ms: 4200, error: "context canceled" }],
      },
    ],
  };
  const { root, unmount } = mount({ state, attemptNumber: 1, events: [] });
  const row = stageRow(root, "implement");
  assert.match(row.textContent, /interrupted/);
  assert.doesNotMatch(row.textContent, /failed/);
  unmount();
});

test("a stage with no recorded start shows no invented duration", () => {
  const state = {
    ...ATTEMPTS,
    attempts: [
      {
        attempt: 1,
        status: "ok",
        stages: [{ name: "commit", seq: 0, status: "ok" }],
      },
    ],
  };
  const { root, unmount } = mount({ state, attemptNumber: 1, events: [] });
  const row = stageRow(root, "commit");
  assert.match(row.textContent, /duration not recorded/);
  assert.doesNotMatch(row.textContent, /ran for/);
  unmount();
});

// F3: waiting_human can span days, so an attempt's wall time is not a duration
// of work and the page must not present it as one.
test("the attempt's own duration is never rendered", () => {
  const { root, unmount } = mount({ state: ATTEMPTS, attemptNumber: 1, events: [] });
  assert.doesNotMatch(root.textContent, /ran for 16 min/);
  assert.doesNotMatch(root.textContent, /1,000,000|1000000/);
  unmount();
});

test("events with no attempt are reported rather than guessed at", () => {
  const { root, unmount } = mount({ state: ATTEMPTS, attemptNumber: 1, events: [] });
  assert.match(root.textContent, /3 events on this task predate attempt attribution/);
  unmount();
});

// F1: a stage is `ok` when its function returned without error. There is no
// exit code and no verification, and the agent's own verdict is a separate
// claim -- neither may be presented as a check result.
test("the agent's own verdict is shown separately from the stage's status", () => {
  const events = [
    {
      kind: "agent_finish",
      attempt: 1,
      stage: "implement",
      data: { status: "passed", iterations: 7, tokens: 1234, model: "openai/gpt-5.6-luna" },
    },
  ];
  const { root, unmount } = mount({ state: ATTEMPTS, attemptNumber: 1, events });
  const row = stageRow(root, "implement");
  assert.match(row.textContent, /Agent's own report, not verified by archie/);
  assert.match(row.textContent, /Agent passed: implement/);
  assert.match(row.textContent, /failed/, "the stage keeps its own terminal status");
  assert.doesNotMatch(row.textContent, /exit code|exit status/i);
  unmount();
});

test("an agent report for another attempt is not attached to this attempt's stage", () => {
  const events = [
    { kind: "agent_finish", attempt: 2, stage: "implement", data: { status: "passed" } },
  ];
  const { root, unmount } = mount({ state: ATTEMPTS, attemptNumber: 1, events });
  assert.doesNotMatch(root.textContent, /Agent's own report/);
  unmount();
});

test("the rail says what ok means instead of implying a gate ran", () => {
  const { root, unmount } = mount({ state: ATTEMPTS, attemptNumber: 1, events: [] });
  assert.match(root.textContent, /ok means the stage returned without error/);
  assert.match(root.textContent, /does not verify/);
  unmount();
});

test("an attempt with no stages says so instead of showing an empty rail", () => {
  const state = {
    task_id: 42,
    current_attempt: 1,
    unattributed_events: 0,
    attempts: [{ attempt: 1, status: "unknown", stages: [] }],
  };
  const { root, unmount } = mount({ state, attemptNumber: 1, events: [] });
  assert.match(root.textContent, /No stages recorded for this attempt/);
  unmount();
});

test("a task with no attempts says so", () => {
  const { root, unmount } = mount({
    state: { task_id: 42, current_attempt: 0, unattributed_events: 0, attempts: [] },
    attemptNumber: null,
    events: [],
  });
  assert.match(root.textContent, /No attempts recorded/);
  unmount();
});

test("a selected attempt that was never recorded is reported, not faked", () => {
  const { root, unmount } = mount({ state: ATTEMPTS, attemptNumber: 9, events: [] });
  assert.match(root.textContent, /Attempt 9 is not recorded for this task/);
  assert.match(root.textContent, /attempt 1, 2/);
  unmount();
});

// A deployment where every event predates the attempt column answers with no
// attempts at all -- but the events are still there, unattributed. The count
// must be surfaced in the EMPTY rail too, not only beside a populated one, or
// the page presents unattributable history as "nothing happened".
test("unattributed events are surfaced even when no attempt is recorded", () => {
  const { root, unmount } = mount({
    state: { task_id: 42, current_attempt: 0, unattributed_events: 4, attempts: [] },
    attemptNumber: null,
    events: [],
  });
  assert.match(root.textContent, /No attempts recorded/);
  assert.match(root.textContent, /4 events on this task predate attempt attribution/);
  unmount();
});

test("unattributed events are surfaced when the selected attempt is not recorded", () => {
  const { root, unmount } = mount({ state: ATTEMPTS, attemptNumber: 9, events: [] });
  assert.match(root.textContent, /Attempt 9 is not recorded/);
  assert.match(root.textContent, /3 events on this task predate attempt attribution/);
  unmount();
});

test("loading and failure states are distinct, and the failure offers a retry", () => {
  const loading = mount({ state: undefined, attemptNumber: null, events: [] });
  assert.match(loading.root.textContent, /Loading/);
  loading.unmount();

  const failed = mount({ state: null, attemptNumber: null, events: [], onRetry: () => {} });
  assert.match(failed.root.textContent, /Could not load this task's attempts/);
  assert.equal(failed.root.querySelector("button").textContent, "Retry");
  failed.unmount();
});

test.after(() => cleanup());
