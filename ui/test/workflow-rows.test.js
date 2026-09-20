import { test } from "node:test";
import assert from "node:assert/strict";
import { workflowRows } from "../src/workflows/workflow-rows.js";

test("a defined workflow with no runs still gets a row at zero", () => {
  const rows = workflowRows([], [{ id: "tdd", name: "TDD", enabled: true }]);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].id, "tdd");
  assert.equal(rows[0].runs, 0);
});

test("statistics are merged onto their definition", () => {
  const rows = workflowRows(
    [{ workflow: "tdd", runs: 3, merged: 1 }],
    [{ id: "tdd", name: "TDD", enabled: true }],
  );
  assert.equal(rows.length, 1);
  assert.equal(rows[0].name, "TDD");
  assert.equal(rows[0].runs, 3);
  assert.equal(rows[0].merged, 1);
});

// The regression: stats arrived, definitions did not, and the page claimed
// there were no runs while the stage tables below listed them.
test("a workflow with runs but no definition still gets a row", () => {
  const rows = workflowRows([{ workflow: "implement", runs: 6, merged: 3 }], []);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].id, "implement");
  assert.equal(rows[0].runs, 6);
});

test("definitions keep their order and stats-only workflows follow", () => {
  const rows = workflowRows(
    [{ workflow: "ghost", runs: 2 }, { workflow: "tdd", runs: 1 }],
    [{ id: "tdd" }, { id: "implement" }],
  );
  assert.deepEqual(rows.map((r) => r.id), ["tdd", "implement", "ghost"]);
});

test("only genuinely empty input produces no rows", () => {
  assert.deepEqual(workflowRows([], []), []);
  assert.deepEqual(workflowRows(undefined, undefined), []);
  assert.deepEqual(workflowRows(null, null), []);
});
