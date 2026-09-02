import { test } from "node:test";
import assert from "node:assert/strict";
import { h } from "preact";
import { render, cleanup } from "@testing-library/preact";
import { Row, valueText, parseEdit } from "../src/settings/config-row.jsx";

// Walks a rendered row and returns the concatenated text of each direct
// child, so a test can assert on the row's *structure* rather than on a
// flattened string. The bugs this file guards against were all cases of
// two fields rendering as one visual run.
function cells(rowEl) {
  return Array.from(rowEl.childNodes).map((child) => text(child));
}

function text(node) {
  return node.textContent ?? "";
}

function classOf(node) {
  return (node && node.className) || "";
}

function titleOf(node) {
  return node && node.getAttribute ? node.getAttribute("title") : undefined;
}

// Render a Row component and return its rendered .kv element.
function renderRow(label, value, opts = {}) {
  const { container, unmount } = render(<Row label={label} value={value} opts={opts} />);
  const el = container.querySelector(".kv");
  return { el, unmount };
}

test("a locked row keeps its reason in a separate element from the value", () => {
  // Regression: the reason used to sit beside an overflowing value and
  // render as "/home/sam/.local/share/archie/workpins the daemon's
  // working layout". Assert on the cell count and per-cell text, never on
  // the row's flattened text, so a future layout change cannot pass by
  // merging them back into one node.
  const { el: rowEl } = renderRow("Work directory", "/home/sam/.local/share/archie/work", {
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
  const { el: rowEl } = renderRow("State path prefix", "/home/sam/.local/share/archie/archie.db", {
    key: "db_path",
    locked: { db_path: "required for bootstrap" },
  });
  assert.equal(Array.from(rowEl.childNodes).filter((c) => classOf(c).includes("kv-actions")).length, 0);
});

test("an editable row separates value from its actions", () => {
  // Regression: the value overflowed into the action column, so "400"
  // rendered as "40" with Edit sitting on top of the last digit. Assert
  // the value and the actions are distinct children.
  const { el: rowEl } = renderRow("Max diff size (lines)", 400, { key: "diff_cap_lines", type: "int", raw: 400 });
  const parts = cells(rowEl);
  assert.equal(parts.length, 3);
  assert.equal(parts[1], "400", "the value must not be merged with the action label");
  assert.equal(parts[2], "Edit");
});

test("an overridden row shows the marker and a reset alongside edit", () => {
  const { el: rowEl } = renderRow("Max steps", 90, {
    key: "budgets.max_steps",
    type: "int",
    raw: 90,
    overridden: ["budgets.max_steps"],
  });
  const actions = Array.from(rowEl.childNodes).find((c) => classOf(c).includes("kv-actions"));
  assert.deepEqual(Array.from(actions.childNodes).map(text), ["overridden", "Edit", "Reset"]);
});

test("the full value is recoverable from the title when the column truncates", () => {
  const long = "go vet ./...  →  go build ./...  →  go test ./... -count=1";
  const { el: rowEl } = renderRow("Quality gate", long);
  const value = Array.from(rowEl.childNodes).find((c) => classOf(c).includes("kv-value"));
  assert.equal(titleOf(value), long);
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

// archie-core-b6ew.3: a schema-driven field with editable: false (a
// structured field with no dedicated editor yet, or a field the backend
// deliberately doesn't allow editing) must not offer an Edit button, even
// though it has a key -- distinct from a locked row, which does have a key
// and a reason.
test("a field marked editable: false renders read-only with no note", () => {
  const { el: rowEl } = renderRow("Model roles", "{...}", { key: "models", type: "structured", editable: false });
  const parts = cells(rowEl);
  assert.equal(parts.length, 2, "expected only label and value, no actions or note");
  assert.ok(!classOf(rowEl).includes("is-locked"));
});

// A locked reason still takes priority over editable: true -- the runtime
// state (overlay.DeniedKeys) wins over the schema's static claim.
test("a locked reason is shown even when the field claims editable: true", () => {
  const { el: rowEl } = renderRow("Work directory", "/work/archie", {
    key: "work_dir",
    editable: true,
    locked: { work_dir: "pins the daemon's working layout; cannot be changed at runtime" },
  });
  assert.ok(classOf(rowEl).includes("is-locked"));
});

// archie-core-b6ew.3: the backend-authored field description rides as a
// tooltip on the label, so a generic renderer can carry "0 means
// unlimited." without any field-specific frontend code.
test("a field's hint becomes the label's title attribute", () => {
  const { el: rowEl } = renderRow("Max steps", 0, { hint: "0 means unlimited." });
  const label = Array.from(rowEl.childNodes).find((c) => classOf(c).includes("kv-label"));
  assert.equal(titleOf(label), "0 means unlimited.");
});

test("a field with no hint renders a plain label with no title", () => {
  const { el: rowEl } = renderRow("Max steps", 0, {});
  const label = Array.from(rowEl.childNodes).find((c) => classOf(c).includes("kv-label"));
  assert.equal(titleOf(label), null);
});
