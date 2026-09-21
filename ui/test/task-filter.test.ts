import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import { computed, ref } from "vue";

import { initialTaskFilter, taskMatchesStatus, taskStatuses } from "../src/tasks/task-filter.ts";

const QUEUED = { id: "queued", label: "Queued", kind: "idle" };
const WAITING = { id: "waiting_human", label: "Waiting for you", kind: "warn", needs_you: true };
const TRIAGING = { id: "triaging", label: "Triaging", kind: "info" };

test("only a status the served catalog knows becomes a filter", () => {
  const catalog = [QUEUED, WAITING, TRIAGING];
  const cases: Array<[string | null | undefined, string]> = [
    ["queued", "queued"],
    ["needs_you", "needs_you"],
    ["triaging", "triaging"],
    ["archived_elsewhere", ""],
    ["   ", ""],
    ["", ""],
    [null, ""],
    [undefined, ""],
  ];
  for (const [requested, expected] of cases) {
    assert.equal(initialTaskFilter(requested, catalog), expected, JSON.stringify(requested));
  }
});

test("needs_you groups exactly the statuses the catalog marks for a human", () => {
  const catalog = [QUEUED, WAITING, TRIAGING];
  assert.deepEqual([...taskStatuses(catalog)], ["needs_you", "queued", "waiting_human", "triaging"]);

  const cases: Array<[{ status?: string }, string, boolean]> = [
    [{ status: "waiting_human" }, "needs_you", true],
    [{ status: "triaging" }, "needs_you", false],
    [{ status: "queued" }, "", true],
    [{ status: "triaging" }, "triaging", true],
    [{ status: "queued" }, "triaging", false],
    [{}, "queued", false],
  ];
  for (const [task, status, expected] of cases) {
    assert.equal(taskMatchesStatus(task, status, catalog), expected, `${task.status} against ${status}`);
  }
});

// The defect: the board captured the filter at setup, before loadTaskMeta()
// replaced the freeze-dried defaults, and the only re-derivation watched the
// query string. A status the server serves but the defaults do not know was
// therefore dropped, and the whole board rendered in place of the shared view.
// Deriving the filter from the catalog instead makes the late catalog a
// re-derivation, and the served id becomes the filter without a reload.
test("a filter is re-derived when the served catalog lands", () => {
  const catalog = ref([QUEUED]);
  const query = ref("triaging");
  const status = computed(() => initialTaskFilter(query.value, catalog.value));

  assert.equal(status.value, "", "an id no catalog has held yet is dropped");
  catalog.value = [...catalog.value, TRIAGING];
  assert.equal(status.value, "triaging", "the served id becomes the filter once it arrives");
  query.value = "queued";
  assert.equal(status.value, "queued", "a back-button step still moves the filter");
  query.value = "";
  assert.equal(status.value, "", "clearing the query clears the filter");
});

test("the task board derives its filter from the query instead of freezing it at setup", async () => {
  const page = await readFile(new URL("../src/tasks/TasksPage.vue", import.meta.url), "utf8");
  assert.match(page, /const status = computed\(\(\) => initialTaskFilter\(statusQuery\.value/);
  assert.doesNotMatch(page, /watch\(statusQuery/);
  assert.doesNotMatch(page, /status\.value = /);
});
