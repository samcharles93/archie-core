import { test } from "node:test";
import assert from "node:assert/strict";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/preact";
import { render as preactRender } from "preact";
import { api, ApiError } from "../src/base/api.jsx";
import { taskDetailPage } from "../src/tasks/task-detail.jsx";

const CONFIG_SCHEMA = "archie/task-config@1";

const TASK = {
  id: 42,
  title: "Fix the flaky test",
  status: "parked",
  owner: "o",
  repo: "r",
  workflow: "tdd",
  attempt: 2,
  actions: ["retry", "abandon", "reject"],
  repo_url: "https://forge.example.internal/o/r",
  issue_url: "https://forge.example.internal/o/r/issues/5",
  issue_number: 5,
  pr_number: 7,
  pr_url: "https://forge.example.internal/o/r/pull/7",
};

const ATTEMPTS = {
  task_id: 42,
  current_attempt: 2,
  unattributed_events: 0,
  attempts: [
    {
      attempt: 1,
      status: "failed",
      started_at: "2026-09-18T07:00:00.000Z",
      finished_at: "2026-09-18T07:04:00.000Z",
      duration_ms: 240333,
      event_count: 3,
      stages: [
        { name: "prepare", seq: 0, status: "ok", started_at: "2026-09-18T07:00:00.123Z", duration_ms: 1200, error: "" },
        { name: "implement", seq: 1, status: "failed", duration_ms: 230000, error: "stage implement: builder exited 1" },
      ],
    },
    {
      attempt: 2,
      status: "running",
      started_at: "2026-09-18T07:05:00.000Z",
      event_count: 1,
      stages: [{ name: "analyse", seq: 0, status: "running", started_at: "2026-09-18T07:05:00.000Z" }],
    },
  ],
};

const EVENTS = [
  {
    kind: "config_captured",
    attempt: 2,
    at: "2026-09-18T07:05:00.100Z",
    stage: "",
    data: { schema: CONFIG_SCHEMA, document: { BotUser: "archie", Models: { main: "openai/gpt" } } },
  },
  { kind: "stage_start", attempt: 2, stage: "analyse", at: "2026-09-18T07:05:00.000Z", data: {} },
];

function install(overrides = {}) {
  const original = {
    tasks: api.tasks,
    task: api.task,
    taskAttempts: api.taskAttempts,
    taskLogs: api.taskLogs,
    taskChanges: api.taskChanges,
    taskDebug: api.taskDebug,
    taskAction: api.taskAction,
  };
  const calls = { tasks: 0, task: 0, attempts: 0, logs: [], changes: [], debug: [], actions: [] };
  api.tasks = async () => {
    calls.tasks += 1;
    return [TASK];
  };
  api.task = async (id) => {
    calls.task += 1;
    return EVENTS;
  };
  api.taskAttempts = async (id) => {
    calls.attempts += 1;
    return ATTEMPTS;
  };
  api.taskLogs = async (id, params) => {
    calls.logs.push(params);
    return { found: true, attempt: params?.attempt ?? 2, disabled: false, entries: [{ level: "info", msg: "line", time: "2026-09-18T07:05:00Z" }] };
  };
  api.taskChanges = async (id, params) => {
    calls.changes.push(params);
    return { task_id: 42, attempt: params?.attempt ?? 2, found: false, captures: [] };
  };
  api.taskDebug = async (id, params) => {
    calls.debug.push(params);
    return { task_id: 42, attempt: params?.attempt ?? 2, task: TASK, events: EVENTS };
  };
  api.taskAction = async (id, action) => {
    calls.actions.push({ id, action });
    return {};
  };
  for (const [key, value] of Object.entries(overrides)) api[key] = value;
  const restore = () => Object.assign(api, original);
  return { calls, restore };
}

function mount(queryString, id = "42") {
  const { container, unmount } = render(
    taskDetailPage(new URLSearchParams(queryString), { id }),
  );
  return { root: container, unmount };
}

test("the page offers every panel as a tab and opens Stages by default", async () => {
  const { restore } = install();
  const { root, unmount } = mount("tab=");
  await waitFor(() => assert.ok(root.querySelector(".run-stages"), "the rail should render"));
  const tabs = [...root.querySelectorAll('[role="tab"]')].map((tab) => tab.textContent);
  assert.deepEqual(tabs, ["Stages", "Log", "Changed files", "Configuration", "Debug"]);
  assert.equal(root.querySelector('[role="tab"]').getAttribute("aria-selected"), "true");
  // The panel is bound to the selected tab.
  const panel = root.querySelector('[role="tabpanel"]');
  assert.equal(panel.getAttribute("aria-labelledby"), root.querySelector('[role="tab"]').id);
  unmount();
  restore();
});

test("the panel element is the selected tab's panel", () => {
  const { root, unmount } = mount("tab=changes");
  const selected = [...root.querySelectorAll('[role="tab"]')].find(
    (tab) => tab.getAttribute("aria-selected") === "true",
  );
  assert.equal(selected.textContent, "Changed files");
  assert.equal(root.querySelector('[role="tabpanel"]').id, selected.getAttribute("aria-controls"));
  unmount();
});

// R8: the tabs are deep-linkable, so the query string alone decides which panel
// opens -- including on a reload with no client state.
test("?tab= decides which panel opens", async () => {
  const { restore, calls } = install();
  const { root, unmount } = mount("tab=debug");
  await waitFor(() => assert.ok(root.querySelector(".run-json"), "the debug view should render"));
  const json = root.querySelector(".run-json").textContent;
  assert.match(json, /"title": "Fix the flaky test"/);
  assert.match(json, /"attempt": 2/);
  assert.match(root.textContent, /Attempt 2 is the one selected above/);
  assert.deepEqual(calls.debug, [{ attempt: 2 }]);
  // The debug envelope is only fetched for the visible tab.
  assert.equal(calls.changes.length, 0);
  unmount();
  restore();
});

test("the debug view states that its event list is unfiltered", async () => {
  const { restore } = install();
  const { root, unmount } = mount("tab=debug");
  await waitFor(() => assert.ok(root.querySelector(".run-json")));
  assert.match(root.textContent, /Events are unfiltered on purpose/);
  unmount();
  restore();
});

test("the default attempt is the task's current one", async () => {
  const { restore } = install();
  const { root, unmount } = mount("tab=stages");
  await waitFor(() => assert.ok(root.querySelector(".run-stages")));
  assert.match(root.textContent, /Attempt 2/);
  assert.equal(
    [...root.querySelectorAll(".run-stage-name")].map((n) => n.textContent).join(","),
    "analyse",
    "attempt 1's stages must not appear on the current attempt's rail",
  );
  const chips = [...root.querySelectorAll(".run-attempt")];
  assert.equal(chips.length, 2);
  assert.equal(chips[1].getAttribute("aria-pressed"), "true");
  unmount();
  restore();
});

test("?attempt= pins an earlier attempt and the rail follows it", async () => {
  const { restore } = install();
  const { root, unmount } = mount("tab=stages&attempt=1");
  await waitFor(() => assert.ok(root.querySelector(".run-stages")));
  assert.deepEqual(
    [...root.querySelectorAll(".run-stage-name")].map((n) => n.textContent),
    ["prepare", "implement"],
  );
  assert.equal(root.querySelectorAll(".run-attempt")[0].getAttribute("aria-pressed"), "true");
  unmount();
  restore();
});

test("selecting another attempt re-reads the panels for that attempt", async () => {
  const { restore } = install();
  const { root, unmount } = mount("tab=stages");
  await waitFor(() => assert.ok(root.querySelector(".run-stages")));
  fireEvent.click(root.querySelectorAll(".run-attempt")[0]);
  await waitFor(() =>
    assert.deepEqual(
      [...root.querySelectorAll(".run-stage-name")].map((n) => n.textContent),
      ["prepare", "implement"],
    ),
  );
  unmount();
  restore();
});

test("the log pane sends the selected attempt and the filters to the server", async () => {
  const { restore, calls } = install();
  const { root, unmount } = mount("tab=log&attempt=1");
  await waitFor(() => assert.ok(root.querySelector(".log-list")));
  assert.deepEqual(calls.logs, [{ attempt: 1, level: "", stage: "", limit: 500 }]);

  const stageSelect = [...root.querySelectorAll(".task-log-filter select")][1];
  fireEvent.change(stageSelect, { target: { value: "prepare" } });
  await waitFor(() => assert.equal(calls.logs.length, 2));
  assert.deepEqual(calls.logs[1], { attempt: 1, level: "", stage: "prepare", limit: 500 });
  await waitFor(() => assert.match(root.textContent, /A stage filter matches only log lines that record a stage/));
  unmount();
  restore();
});

// R4: the Config tab consumes the events response the page already holds, so it
// must issue no request of its own.
test("the configuration tab reads the captured document from the fetched events", async () => {
  const { restore, calls } = install();
  const { root, unmount } = mount("tab=config");
  await waitFor(() => assert.ok(root.querySelector(".run-json")));
  assert.match(root.querySelector(".run-json").textContent, /"BotUser": "archie"/);
  assert.equal(calls.task, 1, "the events are fetched once, by the page");
  assert.equal(calls.changes.length, 0);
  assert.equal(calls.debug.length, 0);
  unmount();
  restore();
});

test("the configuration tab says nothing was captured for an attempt with no record", async () => {
  const { restore } = install();
  const { root, unmount } = mount("tab=config&attempt=1");
  await waitFor(() =>
    assert.equal(root.querySelector(".empty-title").textContent, "Not captured for this run"),
  );
  unmount();
  restore();
});

test("changed files are read for the selected attempt and say a capture is missing", async () => {
  const { restore, calls } = install();
  const { root, unmount } = mount("tab=changes&attempt=1");
  await waitFor(() =>
    assert.equal(root.querySelector(".empty-title").textContent, "No change capture was recorded for this attempt"),
  );
  assert.deepEqual(calls.changes, [{ attempt: 1 }]);
  unmount();
  restore();
});

// R6: the control is labelled as a new run, and the copy says what it destroys.
test("the new-run control states that the previous attempt's commits are discarded", async () => {
  const { restore } = install();
  const { root, unmount } = mount("tab=stages");
  await waitFor(() => assert.ok(root.querySelector(".run-restart")));
  const button = root.querySelector(".run-restart button");
  assert.equal(button.textContent, "Start a new run");
  assert.match(root.textContent, /The previous attempt's commits are discarded/);
  assert.match(root.textContent, /fresh worktree reset onto the base branch/);
  unmount();
  restore();
});

test("a declined confirmation starts no run", async () => {
  const { restore, calls } = install();
  const originalConfirm = window.confirm;
  let asked = "";
  window.confirm = (text) => {
    asked = text;
    return false;
  };
  const { root, unmount } = mount("tab=stages");
  await waitFor(() => assert.ok(root.querySelector(".run-restart button")));
  fireEvent.click(root.querySelector(".run-restart button"));
  assert.match(asked, /previous attempt's commits are discarded/);
  assert.deepEqual(calls.actions, []);
  unmount();
  window.confirm = originalConfirm;
  restore();
});

test("a confirmed new run drops the old attempt's caches and selects the new run", async () => {
  const NEW_ATTEMPTS = {
    task_id: 42,
    current_attempt: 3,
    unattributed_events: 0,
    attempts: [
      ...ATTEMPTS.attempts,
      { attempt: 3, status: "running", started_at: "2026-09-18T07:09:00.000Z", event_count: 1, stages: [{ name: "bootstrap", seq: 0, status: "ok", duration_ms: 500 }] },
    ],
  };
  let attemptReads = 0;
  const { restore, calls } = install({
    taskAttempts: async () => {
      attemptReads += 1;
      return attemptReads === 1 ? ATTEMPTS : NEW_ATTEMPTS;
    },
  });
  const originalConfirm = window.confirm;
  window.confirm = () => true;
  const { root, unmount } = mount("tab=stages");
  await waitFor(() => assert.ok(root.querySelector(".run-stages")));
  fireEvent.click(root.querySelector(".run-restart button"));
  await waitFor(() => assert.equal(root.querySelectorAll(".run-attempt").length, 3));
  await waitFor(() =>
    assert.deepEqual(
      [...root.querySelectorAll(".run-stage-name")].map((n) => n.textContent),
      ["bootstrap"],
      "the new run's stages are shown, not the previous attempt's",
    ),
  );
  assert.deepEqual(calls.actions, [{ id: 42, action: "retry" }]);
  assert.equal(root.querySelectorAll(".run-attempt")[2].getAttribute("aria-pressed"), "true");
  assert.equal(calls.task, 2, "the ended attempt's events are refetched for the new run");
  unmount();
  window.confirm = originalConfirm;
  restore();
});

test("a task outside the listed rows is reported honestly rather than blank", async () => {
  const { restore } = install({ tasks: async () => [] });
  const { root, unmount } = mount("tab=stages");
  await waitFor(() => assert.ok(root.querySelector(".run-stages")));
  assert.match(root.textContent, /not among the tasks this dashboard lists/);
  assert.match(root.textContent, /Debug tab holds the stored record verbatim/);
  assert.match(root.textContent, /operator controls could not be read/);
  unmount();
  restore();
});

test("a deployment that lists no tasks for this id still shows the recorded run", async () => {
  const { restore } = install({ tasks: async () => [] });
  const { root, unmount } = mount("tab=stages&attempt=1");
  await waitFor(() => assert.ok(root.querySelector(".run-stages")));
  assert.match(root.textContent, /Task #42/);
  assert.match(root.textContent, /prepare/);
  unmount();
  restore();
});

test("an unknown task renders an honest failure, not an empty page", async () => {
  const { restore } = install({
    taskAttempts: async () => {
      throw new ApiError("no such task", 404);
    },
  });
  const { root, unmount } = mount("tab=stages");
  await waitFor(() => assert.ok(root.querySelector(".page-title")));
  assert.match(root.querySelector(".page-title").textContent, /Task not found/);
  assert.match(root.textContent, /No task with id 42/);
  unmount();
  restore();
});

test("a non-numeric id is refused without asking the server anything", () => {
  const { restore, calls } = install();
  const { root, unmount } = mount("tab=stages", "not-an-id");
  assert.match(root.textContent, /is not a task id/);
  assert.deepEqual(calls, { tasks: 0, task: 0, attempts: 0, logs: [], changes: [], debug: [], actions: [] });
  unmount();
  restore();
});

test("a task with no attempts says so on every panel", async () => {
  const { restore } = install({
    taskAttempts: async () => ({ task_id: 42, current_attempt: 0, unattributed_events: 4, attempts: [] }),
  });
  const { root, unmount } = mount("tab=stages");
  await waitFor(() => assert.match(root.textContent, /No attempts recorded/));
  fireEvent.click([...root.querySelectorAll('[role="tab"]')][3]);
  await waitFor(() => assert.match(root.textContent, /No attempt recorded/));
  unmount();
  restore();
});

test("a failure to load the attempts offers a retry", async () => {
  let reads = 0;
  const { restore } = install({
    taskAttempts: async () => {
      reads += 1;
      if (reads === 1) throw new Error("boom");
      return ATTEMPTS;
    },
  });
  const { root, unmount } = mount("tab=stages");
  await waitFor(() => assert.match(root.textContent, /Could not load this task's attempts/));
  fireEvent.click([...root.querySelectorAll(".run-error button")][0]);
  await waitFor(() => assert.ok(root.querySelector(".run-stages")));
  unmount();
  restore();
});

// The router renders the run page with Preact's own render() into one long-lived
// outlet (main.jsx, show()), so moving from one task id to another diffs the same
// component in place rather than mounting a fresh one. Every piece of task-scoped
// state therefore has to be re-established when the id changes -- otherwise the
// new task is shown with the previous task's tab, pinned attempt, run history and
// events. RunDetail's own comment relies on this ("the route remounts it when
// that changes"), so navigating between two ids is the case to pin.
test("navigating to another task id re-establishes the task-scoped reads and selections", async () => {
  const attemptReads = [];
  const eventReads = [];
  const { restore } = install({
    taskAttempts: async (id) => {
      attemptReads.push(String(id));
      return ATTEMPTS;
    },
    task: async (id) => {
      eventReads.push(String(id));
      return EVENTS;
    },
  });
  const host = document.createElement("div");
  document.body.appendChild(host);

  // Task 1, opened pinned to its earlier attempt and to the Log tab.
  preactRender(taskDetailPage(new URLSearchParams("tab=log&attempt=1"), { id: "1" }), host);
  await waitFor(() => assert.equal(attemptReads.length, 1));
  const selectedTab = () =>
    [...host.querySelectorAll('[role="tab"]')].find(
      (tab) => tab.getAttribute("aria-selected") === "true",
    );
  const pressedAttempt = () =>
    [...host.querySelectorAll(".run-attempt")].find(
      (chip) => chip.getAttribute("aria-pressed") === "true",
    );
  assert.equal(selectedTab().textContent, "Log");
  assert.match(pressedAttempt().textContent, /Attempt 1/);

  // The router's own navigation: the same matched route, a different captured id,
  // the same outlet. Nothing unmounts, so no state is cleared for free.
  preactRender(taskDetailPage(new URLSearchParams(""), { id: "2" }), host);

  await waitFor(() =>
    assert.equal(attemptReads.length, 2, "the new task's run history must be re-read"),
  );
  assert.equal(attemptReads[1], "2", "the run history must be read for the new task's id");
  assert.equal(eventReads.length, 2, "the new task's events must be re-read");
  assert.equal(eventReads[1], "2", "the events must be read for the new task's id");

  assert.equal(
    selectedTab().textContent,
    "Stages",
    "the previous task's tab must not carry over to the new task",
  );
  assert.match(
    pressedAttempt().textContent,
    /Attempt 2/,
    "the previous task's pinned attempt must not carry over to the new task",
  );

  preactRender(null, host);
  host.remove();
  restore();
});

test.after(() => cleanup());
