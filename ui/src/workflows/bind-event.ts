import { constantValue, type Binding, type InputSource } from "@/bindings/binding-draft";
import type { Capture } from "@/captures/state";
import type { FieldType } from "@/mappings/mapping-fields";
import type { MappingField, Preview } from "@/mappings/state";

/**
 * Binding an event to one workflow from its Start node: each declared input
 * reads a path in the event's payload or takes a fixed value, and the paths
 * become the binding's mapping.
 */

export interface InputSpec {
  type?: string;
  required?: boolean;
}

/** How one input is filled: from a payload path, or a fixed value. */
export interface InputRow {
  from: "path" | "value";
  path: string;
  value: string;
}

/** The mapping field a repository path is bound under. */
export const REPOSITORY_FIELD = "repository_path";

/** The mapping fields the rows bind: one per input read from the payload,
 * named after the input, and the repository path when one is given. */
export function mappingFields(inputs: [string, InputSpec][], rows: Record<string, InputRow>, repositoryPath: string): MappingField[] {
  const fields: MappingField[] = [];
  for (const [name, spec] of inputs) {
    const row = rows[name];
    if (row?.from !== "path" || !row.path.trim()) continue;
    fields.push({ name, path: row.path.trim(), type: (spec.type ?? "string") as FieldType, required: spec.required === true });
  }
  if (repositoryPath.trim()) fields.push({ name: REPOSITORY_FIELD, path: repositoryPath.trim(), type: "string", required: true });
  return fields;
}

function valueType(value: unknown): string {
  if (Array.isArray(value)) return "array";
  if (value === null) return "null";
  if (typeof value === "boolean") return "bool";
  return typeof value === "object" ? "object" : typeof value;
}

/** What stops the binding saving, each naming its input; empty when it can. */
export function problems(inputs: [string, InputSpec][], rows: Record<string, InputRow>, preview: Preview | null): string[] {
  const out: string[] = [];
  for (const [name, spec] of inputs) {
    const row = rows[name];
    const type = spec.type ?? "string";
    const set = row && (row.from === "path" ? row.path.trim() : row.value !== "");
    if (!set) {
      if (spec.required) out.push(`${name} is required`);
      continue;
    }
    if (row.from === "path") {
      const failure = preview?.failures?.find((f) => f.field_name === name);
      if (failure) out.push(`${name}: ${failure.reason}`);
      continue;
    }
    const got = valueType(constantValue(row.value, type));
    if (type !== "any" && got !== type) out.push(`${name} is ${type}, but ${JSON.stringify(row.value)} is not`);
  }
  if (preview?.failures?.some((f) => f.field_name === REPOSITORY_FIELD)) out.push("the repository path does not resolve");
  return out;
}

/** The binding's inputs: a mapped parameter for each path, a constant for
 * each fixed value. */
export function bindingInputs(inputs: [string, InputSpec][], rows: Record<string, InputRow>): Record<string, InputSource> {
  const out: Record<string, InputSource> = {};
  for (const [name, spec] of inputs) {
    const row = rows[name];
    if (row?.from === "path" && row.path.trim()) out[name] = { param: name };
    else if (row?.from === "value" && row.value !== "") out[name] = { value: constantValue(row.value, spec.type ?? "string") };
  }
  return out;
}

/** The rows an existing binding fills, from its inputs and its mapping. */
export function rowsFromBinding(binding: Binding, fields: MappingField[]): { rows: Record<string, InputRow>; repositoryPath: string } {
  const pathOf = (param?: string) => fields.find((f) => f.name === param)?.path ?? "";
  const rows: Record<string, InputRow> = {};
  for (const [name, src] of Object.entries(binding.inputs ?? {})) {
    rows[name] = src.param
      ? { from: "path", path: pathOf(src.param), value: "" }
      : { from: "value", path: "", value: typeof src.value === "string" ? src.value : JSON.stringify(src.value) };
  }
  return { rows, repositoryPath: pathOf(binding.repo_param) };
}

/** Every path in a payload, leaves and containers, in document order. */
export function payloadPaths(payload: unknown, prefix = ""): string[] {
  if (Array.isArray(payload)) {
    return payload.slice(0, 3).flatMap((item, i) => {
      const path = `${prefix}[${i}]`;
      return [path, ...payloadPaths(item, path)];
    });
  }
  if (payload && typeof payload === "object") {
    return Object.entries(payload).flatMap(([key, value]) => {
      const path = prefix ? `${prefix}.${key}` : key;
      return [path, ...payloadPaths(value, path)];
    });
  }
  return [];
}

/** The capture's body, or null when it is not JSON. */
export function captureBody(capture?: Capture): unknown {
  try {
    return capture?.body ? JSON.parse(capture.body) : null;
  } catch {
    return null;
  }
}

/** The capture's headers as the event-type example takes them. */
export function captureHeaders(capture: Capture): Record<string, string> {
  try {
    const parsed = JSON.parse(capture.headers ?? "{}") as Record<string, unknown>;
    return Object.fromEntries(Object.entries(parsed).map(([k, v]) => [k, Array.isArray(v) ? v.join(", ") : String(v)]));
  } catch {
    return {};
  }
}

/** A preview value as one short line. */
export function shortValue(value: unknown): string {
  const text = typeof value === "string" ? value : JSON.stringify(value);
  return text.length > 80 ? `${text.slice(0, 80)}…` : text;
}
