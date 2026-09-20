/**
 * The field-mapping vocabulary and its pure path/type logic, split out of the
 * components so it can be reasoned about and tested without their fetch
 * wiring -- the split the Preact build made with mapping-editor.jsx.
 */

/** The JSON shapes a bound field can claim, matching mapping.FieldType in Go. */
export type FieldType = "string" | "number" | "bool" | "object" | "array" | "any";

export const FIELD_TYPES: FieldType[] = ["string", "number", "bool", "object", "array", "any"];

const FIELD_TYPE_SET: ReadonlySet<string> = new Set(FIELD_TYPES);

/** One value clicked in the payload tree, ready to become a bound field. */
export interface FieldPick {
  path: string;
  type: FieldType;
  name: string;
}

/**
 * Infers the FieldType a freshly-clicked example value suggests. A null value
 * claims nothing about its shape, so it binds as `any` rather than as a type
 * the payload did not actually demonstrate.
 */
export function fieldTypeFromValue(value: unknown): FieldType {
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

/** Narrows a control's value back to a FieldType, refusing anything else. */
export function asFieldType(value: unknown): FieldType {
  return typeof value === "string" && FIELD_TYPE_SET.has(value) ? (value as FieldType) : "any";
}

export function pathAppendKey(path: string, key: string): string {
  return path ? `${path}.${key}` : key;
}

export function pathAppendIndex(path: string, index: number): string {
  return `${path}[${index}]`;
}

/**
 * A field name no other bound field is using. The daemon rejects a mapping
 * whose fields repeat a name -- a duplicate would let one field silently
 * overwrite another's resolved value at resolve time -- so a click that
 * collides is suffixed rather than refused.
 */
export function uniqueFieldName(taken: Iterable<string>, name: string): string {
  const existing = new Set(taken);
  const base = name || "field";
  let candidate = base;
  let n = 1;
  while (existing.has(candidate)) candidate = `${base}_${++n}`;
  return candidate;
}
