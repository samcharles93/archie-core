import { h, Fragment } from "preact";
import { Pill } from "../base/pill.jsx";

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
export function PayloadTree({ value, path = "", name = "", onPick }) {
  if (value !== null && typeof value === "object") {
    const entries = Array.isArray(value)
      ? value.map((v, i) => [String(i), v, pathAppendIndex(path, i)])
      : Object.entries(value).map(([k, v]) => [k, v, pathAppendKey(path, k)]);
    return (
      <ul className="mapping-tree-list">
        {entries.map(([key, child, childPath]) => (
          <li className="mapping-tree-item" key={childPath}>
            <span 
              className="mapping-tree-key" 
              onClick={(e) => { 
                e.stopPropagation(); 
                if (onPick) onPick({ path: childPath, type: fieldTypeFromValue(child), name: key }); 
              }}
            >
              {key}
            </span>
            <PayloadTree value={child} path={childPath} name={key} onPick={onPick} />
          </li>
        ))}
      </ul>
    );
  }
  return (
    <span 
      className="mapping-tree-value" 
      onClick={(e) => { 
        e.stopPropagation(); 
        if (onPick) onPick({ path, type: fieldTypeFromValue(value), name }); 
      }}
    >
      {JSON.stringify(value)}
    </span>
  );
}

/**
 * Renders the bound-fields table. preview is { values, failures } | null --
 * null means "not previewed yet" and every row shows a neutral status
 * rather than a false pass, since an unresolved field is not the same
 * claim as a resolved one.
 */
export function FieldsTable({ fields, preview, onRemove, onNameChange, onTypeChange, onRequiredChange }) {
  if (!fields.length) {
    return <div className="mapping-empty-fields">Click a value in the payload to bind a field.</div>;
  }
  const failureByField = new Map((preview?.failures || []).map((f) => [f.field_name, f]));
  return (
    <table className="table mapping-fields-table">
      <thead>
        <tr>
          {["Field name", "Path", "Type", "Required", "Preview", ""].map((h, i) => (
            <th key={i}>{h}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {fields.map((f, i) => (
          <tr key={i}>
            <td>
              <input 
                className="mapping-input" 
                value={f.name} 
                onInput={(e) => onNameChange?.(i, e.target.value)} 
              />
            </td>
            <td className="mono">{f.path}</td>
            <td>
              <select 
                className="mapping-select" 
                value={f.type} 
                onChange={(e) => onTypeChange?.(i, e.target.value)}
              >
                {FIELD_TYPES.map((t) => (
                  <option key={t} value={t}>{t}</option>
                ))}
              </select>
            </td>
            <td>
              <input 
                type="checkbox" 
                checked={f.required} 
                onChange={(e) => onRequiredChange?.(i, e.target.checked)} 
              />
            </td>
            <td>
              <PreviewStatus field={f} preview={preview} failureByField={failureByField} />
            </td>
            <td>
              <button className="btn btn-small" type="button" onClick={() => onRemove?.(i)}>Remove</button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function PreviewStatus({ field, preview, failureByField }) {
  if (!preview) return <Pill text="not previewed" kind="idle" />;
  const failure = failureByField.get(field.name);
  if (failure) return <Pill text={failure.reason} kind="danger" />;
  if (Object.prototype.hasOwnProperty.call(preview.values || {}, field.name)) {
    return <Pill text={JSON.stringify(preview.values[field.name])} kind="ok" />;
  }
  return <Pill text="skipped (optional)" kind="idle" />;
}

/** One saved mapping's summary row for the mapping list. */
export function MappingRow({ mapping: m, onEdit, onDelete }) {
  return (
    <tr>
      <td>{m.name}</td>
      <td className="mono">{m.source_hint || "—"}</td>
      <td>{String(m.fields?.length ?? 0)}</td>
      <td>
        <button className="btn btn-small" type="button" onClick={() => onEdit?.(m)}>Edit</button>
        <button className="btn btn-small" type="button" onClick={() => onDelete?.(m)}>Delete</button>
      </td>
    </tr>
  );
}
