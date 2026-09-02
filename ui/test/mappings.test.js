import { test } from "node:test";
import assert from "node:assert/strict";
import { h } from "preact";
import { render } from "@testing-library/preact";
import {
  fieldTypeFromValue,
  FieldsTable,
  MappingRow,
  pathAppendIndex,
  pathAppendKey,
  PayloadTree,
} from "../src/mappings/mapping-editor.jsx";

test("fieldTypeFromValue infers a FieldType from an example value", () => {
  assert.equal(fieldTypeFromValue("x"), "string");
  assert.equal(fieldTypeFromValue(1), "number");
  assert.equal(fieldTypeFromValue(true), "bool");
  assert.equal(fieldTypeFromValue({}), "object");
  assert.equal(fieldTypeFromValue([]), "array");
  assert.equal(fieldTypeFromValue(null), "any");
  assert.equal(fieldTypeFromValue(undefined), "any");
});

test("pathAppendKey joins with a dot, or starts fresh from an empty path", () => {
  assert.equal(pathAppendKey("", "issue"), "issue");
  assert.equal(pathAppendKey("issue", "title"), "issue.title");
});

test("pathAppendIndex appends a bracketed index", () => {
  assert.equal(pathAppendIndex("items", 0), "items[0]");
  assert.equal(pathAppendIndex("", 2), "[2]");
});

test("payloadTree renders every object key", () => {
  const { container } = render(h(PayloadTree, { value: { issue: { title: "bug found" }, count: 3 } }));
  assert.match(container.textContent, /issue/);
  assert.match(container.textContent, /title/);
  assert.match(container.textContent, /"bug found"/);
  assert.match(container.textContent, /count/);
  assert.match(container.textContent, /3/);
});

test("payloadTree renders array indices", () => {
  const { container } = render(h(PayloadTree, { value: { items: [{ id: 1 }, { id: 2 }] } }));
  assert.match(container.textContent, /items/);
  assert.match(container.textContent, /id/);
});

test("fieldsTable shows a hint instead of a table when there are no fields", () => {
  const { container } = render(h(FieldsTable, { fields: [], preview: null }));
  assert.match(container.textContent, /Click a value in the payload/);
});

test("fieldsTable shows 'not previewed' for every field before a preview runs", () => {
  const fields = [{ name: "title", path: "issue.title", type: "string", required: true }];
  const { container } = render(h(FieldsTable, { fields, preview: null }));
  assert.match(container.textContent, /not previewed/);
});

test("fieldsTable surfaces a resolved value after a successful preview", () => {
  const fields = [{ name: "title", path: "issue.title", type: "string", required: true }];
  const { container } = render(h(FieldsTable, { fields, preview: { values: { title: "bug found" }, failures: [] } }));
  assert.match(container.textContent, /bug found/);
  assert.doesNotMatch(container.textContent, /not previewed/);
});

test("fieldsTable surfaces a failure reason loudly, never a blank value", () => {
  const fields = [{ name: "title", path: "issue.title", type: "string", required: true }];
  const { container } = render(h(FieldsTable, { fields, preview: { values: {}, failures: [{ field_name: "title", path: "issue.title", reason: "missing" }] } }));
  assert.match(container.textContent, /missing/);
});

test("fieldsTable shows every bound field's name and path", () => {
  const fields = [
    { name: "title", path: "issue.title", type: "string", required: true },
    { name: "count", path: "count", type: "number", required: false },
  ];
  const { container } = render(h(FieldsTable, { fields, preview: null }));
  assert.match(container.textContent, /issue\.title/);
  assert.match(container.textContent, /count/);
});

test("mappingRow shows name, source hint, and field count", () => {
  const { container } = render(h(MappingRow, {
    mapping: { id: 1, name: "sentry issue opened", source_hint: "sentry", fields: [{}, {}] },
    onEdit: () => {},
    onDelete: () => {},
  }));
  assert.match(container.textContent, /sentry issue opened/);
  assert.match(container.textContent, /sentry/);
  assert.match(container.textContent, /2/);
});

test("mappingRow shows a placeholder when there is no source hint", () => {
  const { container } = render(h(MappingRow, { mapping: { id: 1, name: "m", fields: [] }, onEdit: () => {}, onDelete: () => {} }));
  assert.match(container.textContent, /—/);
});
