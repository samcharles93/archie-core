import { el, pill } from "../base/dom.jsx";

/**
 * Pure rendering + path/type logic for the mapping editor, split out from
 * mappings.js so it is unit-testable without the page's fetch wiring,
 * mirroring captures/capture-row.js.
 */

const FIELD_TYPES = ["string", "number", "bool", "object", "array", "any"];

/** Infers the FieldType a freshly-clicked example value suggests. */
export function fieldTypeFromValue(value) {
  if (value === null || value === undefined) return "any";
  if (Array.isArray(value)) return "array";
  switch (typeof value) {
    case "string":
      return "string";
    case "number":
      return "number";
    case "boolean":
      return "bool";
    case "object":
      return "object";
    default:
      return "any";
  }
}

export function pathAppendKey(path, key) {
  return path ? `${path}.${key}` : key;
}

export function pathAppendIndex(path, index) {
  return `${path}[${index}]`;
}

/**
 * Renders payload as a clickable tree: every key/index and every leaf calls
 * onPick({ path, type, name }) when clicked, so the editor can bind a field
 * without the operator ever typing a JSON path by hand -- "click through
 * its payload, bind JSON paths" per docs/prds/payload-field-mapping.md.
 */
export function payloadTree(value, { path = "", name = "", onPick } = {}) {
  if (value !== null && typeof value === "object") {
    const entries = Array.isArray(value)
      ? value.map((v, i) => [String(i), v, pathAppendIndex(path, i)])
      : Object.entries(value).map(([k, v]) => [k, v, pathAppendKey(path, k)]);
    return el(
      "ul.mapping-tree-list",
      ...entries.map(([key, child, childPath]) =>
        el(
          "li.mapping-tree-item",
          el(
            "span.mapping-tree-key",
            { onclick: (e) => { e.stopPropagation(); onPick?.({ path: childPath, type: fieldTypeFromValue(child), name: key }); } },
            key,
          ),
          payloadTree(child, { path: childPath, name: key, onPick }),
        ),
      ),
    );
  }
  return el(
    "span.mapping-tree-value",
    { onclick: (e) => { e.stopPropagation(); onPick?.({ path, type: fieldTypeFromValue(value), name }); } },
    JSON.stringify(value),
  );
}

/**
 * Renders the bound-fields table. preview is { values, failures } | null --
 * null means "not previewed yet" and every row shows a neutral status
 * rather than a false pass, since an unresolved field is not the same
 * claim as a resolved one.
 */
export function fieldsTable(fields, { preview, onRemove, onNameChange, onTypeChange, onRequiredChange } = {}) {
  if (!fields.length) {
    return el("div.mapping-empty-fields", "Click a value in the payload to bind a field.");
  }
  const failureByField = new Map((preview?.failures || []).map((f) => [f.field_name, f]));
  return el(
    "table.table.mapping-fields-table",
    el(
      "thead",
      el("tr", ...["Field name", "Path", "Type", "Required", "Preview", ""].map((h) => el("th", h))),
    ),
    el(
      "tbody",
      ...fields.map((f, i) =>
        el(
          "tr",
          el("td", el("input.mapping-input", {
            value: f.name,
            oninput: (e) => onNameChange?.(i, e.target.value),
          })),
          el("td.mono", f.path),
          el(
            "td",
            el(
              "select.mapping-select",
              { onchange: (e) => onTypeChange?.(i, e.target.value) },
              ...FIELD_TYPES.map((t) => el("option", { value: t, selected: t === f.type }, t)),
            ),
          ),
          el("td", el("input", {
            type: "checkbox",
            checked: f.required,
            onchange: (e) => onRequiredChange?.(i, e.target.checked),
          })),
          el("td", previewStatus(f, preview, failureByField)),
          el("td", el("button.btn.btn-small", { type: "button", onclick: () => onRemove?.(i) }, "Remove")),
        ),
      ),
    ),
  );
}

function previewStatus(field, preview, failureByField) {
  if (!preview) return pill("not previewed", "idle");
  const failure = failureByField.get(field.name);
  if (failure) return pill(failure.reason, "danger");
  if (Object.prototype.hasOwnProperty.call(preview.values || {}, field.name)) {
    return pill(JSON.stringify(preview.values[field.name]), "ok");
  }
  return pill("skipped (optional)", "idle");
}

/** One saved mapping's summary row for the mapping list. */
export function mappingRow(m, { onEdit, onDelete }) {
  return el(
    "tr",
    el("td", m.name),
    el("td.mono", m.source_hint || "—"),
    el("td", String(m.fields?.length ?? 0)),
    el(
      "td",
      el("button.btn.btn-small", { type: "button", onclick: () => onEdit?.(m) }, "Edit"),
      el("button.btn.btn-small", { type: "button", onclick: () => onDelete?.(m) }, "Delete"),
    ),
  );
}
