import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { row, valueText, parseEdit } from "../src/settings/config-row.js";

// Walks a rendered row and returns the concatenated text of each direct
// child, so a test can assert on the row's *structure* rather than on a
// flattened string. The bugs this file guards against were all cases of
// two fields rendering as one visual run.
function cells(rowEl) {
  return rowEl.childNodes.map((child) => text(child));
}

function text(node) {
  return node.textContent ?? "";
}

function classOf(node) {
  return node.className || "";
}

test("a locked row keeps its reason in a separate element from the value", () => {
  // Regression: the reason used to sit beside an overflowing value and
  // render as "/home/sam/.local/share/archie/workpins the daemon's
  // working layout". Assert on the cell count and per-cell text, never on
  // the row's flattened text, so a future layout change cannot pass by
  // merging them back into one node.
  const rowEl = row("Work directory", "/home/sam/.local/share/archie/work", {
    key: "work_dir",
    locked: { work_dir: "pins the daemon's working layout; cannot be changed at runtime" },
  });

  const parts = cells(rowEl);
  assert.equal(parts.length, 3, "expected label, value and note as separate children");
  assert.equal(parts[0], "Work directory");
  assert.equal(parts[1], "/home/sam/.local/share/archie/work");
  assert.equal(parts[2], "pins the daemon's working layout; cannot be changed at runtime");
  assert.ok(classOf(rowEl).includes("is-locked"));
});

test("a locked row offers no edit control", () => {
  const rowEl = row("State path prefix", "/home/sam/.local/share/archie/archie.db", {
    key: "db_path",
    locked: { db_path: "required for bootstrap" },
  });
  assert.equal(rowEl.childNodes.filter((c) => classOf(c).includes("kv-actions")).length, 0);
});

test("an editable row separates value from its actions", () => {
  // Regression: the value overflowed into the action column, so "400"
  // rendered as "40" with Edit sitting on top of the last digit. Assert
  // the value and the actions are distinct children.
  const rowEl = row("Max diff size (lines)", 400, { key: "diff_cap_lines", type: "int", raw: 400 });
  const parts = cells(rowEl);
  assert.equal(parts.length, 3);
  assert.equal(parts[1], "400", "the value must not be merged with the action label");
  assert.equal(parts[2], "Edit");
});

test("an overridden row shows the marker and a reset alongside edit", () => {
  const rowEl = row("Max steps", 90, {
    key: "budgets.max_steps",
    type: "int",
    raw: 90,
    overridden: ["budgets.max_steps"],
  });
  const actions = rowEl.childNodes.find((c) => classOf(c).includes("kv-actions"));
  assert.deepEqual(actions.childNodes.map(text), ["overridden", "Edit", "Reset"]);
});

test("the full value is recoverable from the title when the column truncates", () => {
  const long = "go vet ./...  →  go build ./...  →  go test ./... -count=1";
  const rowEl = row("Quality gate", long);
  const value = rowEl.childNodes.find((c) => classOf(c).includes("kv-value"));
  assert.equal(value.attrs.title, long);
});

test("zero and false are real values, not missing ones", () => {
  // `value ?? "—"` handled these, but a `value || "—"` refactor would
  // silently turn a configured 0 into "not set". Pin the distinction.
  assert.equal(valueText(0), "0");
  assert.equal(valueText(false), "false");
  assert.equal(valueText(""), "—");
  assert.equal(valueText(null), "—");
  assert.equal(valueText(undefined), "—");
});

test("int edits reject a non-numeric entry before the round trip", () => {
  assert.equal(parseEdit("int", "12").value, 12);
  assert.ok(parseEdit("int", "twelve").error);
  assert.equal(parseEdit("bool", "true").value, true);
  assert.equal(parseEdit("bool", "false").value, false);
  assert.equal(parseEdit("string", "1h0m0s").value, "1h0m0s");
});
