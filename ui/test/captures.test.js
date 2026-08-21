import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { captureRow, captureDetailRow, prettyPrint } from "../src/captures/capture-row.js";

test("prettyPrint indents valid JSON", () => {
  const out = prettyPrint('{"action":"opened","n":1}');
  assert.equal(out, JSON.stringify({ action: "opened", n: 1 }, null, 2));
});

test("prettyPrint returns non-JSON payloads unchanged rather than failing", () => {
  assert.equal(prettyPrint("not json"), "not json");
});

test("collapsed capture row renders only the summary row", () => {
  const nodes = captureRow(
    { id: 1, source: "github", content_type: "application/json" },
    { expanded: false, onToggle: () => {} },
  );
  assert.equal(nodes.length, 1);
});

test("expanded capture row appends a detail row", () => {
  const nodes = captureRow(
    { id: 1, source: "github", content_type: "application/json" },
    { expanded: true, onToggle: () => {} },
  );
  assert.equal(nodes.length, 2);
});

test("capture row reflects expanded state via aria-expanded", () => {
  const [collapsedRow] = captureRow({ id: 1 }, { expanded: false, onToggle: () => {} });
  const [expandedRow] = captureRow({ id: 1 }, { expanded: true, onToggle: () => {} });
  assert.equal(collapsedRow.getAttribute("aria-expanded"), "false");
  assert.equal(expandedRow.getAttribute("aria-expanded"), "true");
});

test("capture row shows the source and content type", () => {
  const [row] = captureRow(
    { id: 1, source: "github", content_type: "application/json" },
    { expanded: false, onToggle: () => {} },
  );
  assert.match(row.textContent, /github/);
  assert.match(row.textContent, /application\/json/);
});

test("capture row shows an unknown source as a placeholder rather than blank", () => {
  const [row] = captureRow({ id: 1 }, { expanded: false, onToggle: () => {} });
  assert.match(row.textContent, /\(unknown\)/);
});

test("every capture row shows Unbound: no binding data model exists yet (t2db.4)", () => {
  const [row] = captureRow({ id: 1, source: "github" }, { expanded: false, onToggle: () => {} });
  assert.match(row.textContent, /Unbound/);
});

test("capture detail row pretty-prints a JSON payload", () => {
  const detail = captureDetailRow({ body: '{"a":1}', headers: "{}" });
  assert.match(detail.textContent, /"a": 1/);
});

test("capture detail row shows a placeholder for an empty payload", () => {
  const detail = captureDetailRow({ body: "", headers: "" });
  assert.match(detail.textContent, /\(empty\)/);
});
