import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import { computed, ref } from "vue";

import { boardStatus, initialTaskFilter, taskMatchesStatus, taskStatuses } from "../src/tasks/task-filter.ts";

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
// re-derivation, and the served id becomes the filter without a reload. This
// runs the board's own derivation -- the page adds only the two arguments.
test("a filter is re-derived when the served catalog lands", () => {
  const catalog = ref([QUEUED]);
  const query = ref("triaging");
  const status = computed(() => boardStatus(query.value, catalog.value));

  assert.equal(status.value, "", "an id no catalog has held yet is dropped");
  catalog.value = [...catalog.value, TRIAGING];
  assert.equal(status.value, "triaging", "the served id becomes the filter once it arrives");
  query.value = "queued";
  assert.equal(status.value, "queued", "a back-button step still moves the filter");
  query.value = "";
  assert.equal(status.value, "", "clearing the query clears the filter");
});

// The page owns only the wiring: which query, and which catalog, the derivation
// is handed. This is therefore asserted as the whole argument list rather than
// the expression's prefix -- a prefix anchor accepted any trailing argument, so
// `const catalog = statusList()` captured at setup and passed to the same call
// kept the old guard green while reintroducing the defect. The check is
// source-level because node --test ships no SFC loader: a .vue file is readable
// here and never executable, which is why the derivation itself is a function
// in task-filter.ts with its own behavioural coverage above.
test("the task board reads the served catalog when the filter re-derives", async () => {
  const page = await readFile(new URL("../src/tasks/TasksPage.vue", import.meta.url), "utf8");
  const wiring = /const status = computed\(\(\) => boardStatus\((.+?)\)\);/.exec(page.replace(/\s+/g, " "));
  assert.equal(wiring?.[1], "statusQuery.value, statusList()", "the filter must be derived, not captured");
});
