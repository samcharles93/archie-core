import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import {
  fieldTypeFromValue,
  fieldsTable,
  mappingRow,
  pathAppendIndex,
  pathAppendKey,
  payloadTree,
} from "../src/mappings/mapping-editor.js";

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
  const tree = payloadTree({ issue: { title: "bug found" }, count: 3 });
  assert.match(tree.textContent, /issue/);
  assert.match(tree.textContent, /title/);
  assert.match(tree.textContent, /"bug found"/);
  assert.match(tree.textContent, /count/);
  assert.match(tree.textContent, /3/);
});

test("payloadTree renders array indices", () => {
  const tree = payloadTree({ items: [{ id: 1 }, { id: 2 }] });
  assert.match(tree.textContent, /items/);
  assert.match(tree.textContent, /id/);
});

test("fieldsTable shows a hint instead of a table when there are no fields", () => {
  const node = fieldsTable([], {});
  assert.match(node.textContent, /Click a value in the payload/);
});

test("fieldsTable shows 'not previewed' for every field before a preview runs", () => {
  const fields = [{ name: "title", path: "issue.title", type: "string", required: true }];
  const node = fieldsTable(fields, { preview: null });
  assert.match(node.textContent, /not previewed/);
});

test("fieldsTable surfaces a resolved value after a successful preview", () => {
  const fields = [{ name: "title", path: "issue.title", type: "string", required: true }];
  const node = fieldsTable(fields, { preview: { values: { title: "bug found" }, failures: [] } });
  assert.match(node.textContent, /bug found/);
  assert.doesNotMatch(node.textContent, /not previewed/);
});

test("fieldsTable surfaces a failure reason loudly, never a blank value", () => {
  const fields = [{ name: "title", path: "issue.title", type: "string", required: true }];
  const node = fieldsTable(fields, {
    preview: { values: {}, failures: [{ field_name: "title", path: "issue.title", reason: "missing" }] },
  });
  assert.match(node.textContent, /missing/);
});

test("fieldsTable shows every bound field's name and path", () => {
  const fields = [
    { name: "title", path: "issue.title", type: "string", required: true },
    { name: "count", path: "count", type: "number", required: false },
  ];
  const node = fieldsTable(fields, { preview: null });
  assert.match(node.textContent, /issue\.title/);
  assert.match(node.textContent, /count/);
});

test("mappingRow shows name, source hint, and field count", () => {
  const row = mappingRow(
    { id: 1, name: "sentry issue opened", source_hint: "sentry", fields: [{}, {}] },
    { onEdit: () => {}, onDelete: () => {} },
  );
  assert.match(row.textContent, /sentry issue opened/);
  assert.match(row.textContent, /sentry/);
  assert.match(row.textContent, /2/);
});

test("mappingRow shows a placeholder when there is no source hint", () => {
  const row = mappingRow({ id: 1, name: "m", fields: [] }, { onEdit: () => {}, onDelete: () => {} });
  assert.match(row.textContent, /—/);
});
