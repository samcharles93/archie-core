import { test } from "node:test";
import assert from "node:assert/strict";
import { h } from "preact";
import { render, cleanup } from "@testing-library/preact";
import { CaptureRow, CaptureDetailRow, prettyPrint } from "../src/captures/capture-row.jsx";

let container;
const afterRender = (cb) => {
  const r = render(cb());
  container = r.container;
  return container;
};
const reset = () => cleanup();

test("prettyPrint indents valid JSON", () => {
  const out = prettyPrint('{"action":"opened","n":1}');
  assert.equal(out, JSON.stringify({ action: "opened", n: 1 }, null, 2));
});

test("prettyPrint returns non-JSON payloads unchanged rather than failing", () => {
  assert.equal(prettyPrint("not json"), "not json");
});

function renderRow(capture, opts = {}) {
  afterRender(() => (
    <table>
      <tbody>
        <CaptureRow capture={capture} expanded={opts.expanded} onToggle={opts.onToggle} />
      </tbody>
    </table>
  ));
  return container.querySelector("tbody");
}

test("collapsed capture row renders only the summary row", () => {
  const tbody = renderRow(
    { id: 1, source: "github", content_type: "application/json" },
    { expanded: false, onToggle: () => {} },
  );
  assert.equal(tbody.children.length, 1);
  reset();
});

test("expanded capture row appends a detail row", () => {
  const tbody = renderRow(
    { id: 1, source: "github", content_type: "application/json" },
    { expanded: true, onToggle: () => {} },
  );
  assert.equal(tbody.children.length, 2);
  reset();
});

test("capture row reflects expanded state via aria-expanded", () => {
  const coll = renderRow({ id: 1 }, { expanded: false, onToggle: () => {} });
  assert.equal(coll.querySelector(".capture-row").getAttribute("aria-expanded"), "false");
  reset();
  const exp = renderRow({ id: 1 }, { expanded: true, onToggle: () => {} });
  assert.equal(exp.querySelector(".capture-row").getAttribute("aria-expanded"), "true");
  reset();
});

test("capture row shows the source and content type", () => {
  const tbody = renderRow(
    { id: 1, source: "github", content_type: "application/json" },
    { expanded: false, onToggle: () => {} },
  );
  const row = tbody.querySelector(".capture-row");
  assert.match(row.textContent, /github/);
  assert.match(row.textContent, /application\/json/);
  reset();
});

test("capture row shows an unknown source as a placeholder rather than blank", () => {
  const tbody = renderRow({ id: 1 }, { expanded: false, onToggle: () => {} });
  const row = tbody.querySelector(".capture-row");
  assert.match(row.textContent, /\(unknown\)/);
  reset();
});

test("every capture row shows Unbound: no binding data model exists yet (t2db.4)", () => {
  const tbody = renderRow({ id: 1, source: "github" }, { expanded: false, onToggle: () => {} });
  const row = tbody.querySelector(".capture-row");
  assert.match(row.textContent, /Unbound/);
  reset();
});

test("capture detail row pretty-prints a JSON payload", () => {
  afterRender(() => (
    <table>
      <tbody>
        <CaptureDetailRow capture={{ body: '{"a":1}', headers: "{}" }} />
      </tbody>
    </table>
  ));
  const detail = container.querySelector(".capture-detail-row");
  assert.match(detail.textContent, /"a": 1/);
  reset();
});

test("capture detail row shows a placeholder for an empty payload", () => {
  afterRender(() => (
    <table>
      <tbody>
        <CaptureDetailRow capture={{ body: "", headers: "" }} />
      </tbody>
    </table>
  ));
  const detail = container.querySelector(".capture-detail-row");
  assert.match(detail.textContent, /\(empty\)/);
  reset();
});
