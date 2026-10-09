import assert from "node:assert/strict";
import test from "node:test";

import { computed, ref } from "vue";

import {
  boardStatus,
  initialTaskFilter,
} from "../src/tasks/task-filter.ts";

const QUEUED = { id: "queued", label: "Queued", kind: "idle" };
const WAITING = {
  id: "waiting_human",
  label: "Waiting for you",
  kind: "warn",
  needs_you: true,
};
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
    assert.equal(
      initialTaskFilter(requested, catalog),
      expected,
      JSON.stringify(requested),
    );
  }
});

test("a filter is re-derived when the served catalog lands", () => {
  const catalog = ref([QUEUED]);
  const query = ref("triaging");
  const status = computed(() => boardStatus(query.value, catalog.value));

  assert.equal(status.value, "", "an id no catalog has held yet is dropped");
  catalog.value = [...catalog.value, TRIAGING];
  assert.equal(
    status.value,
    "triaging",
    "the served id becomes the filter once it arrives",
  );
  query.value = "queued";
  assert.equal(
    status.value,
    "queued",
    "a back-button step still moves the filter",
  );
  query.value = "";
  assert.equal(status.value, "", "clearing the query clears the filter");
});
