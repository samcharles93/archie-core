/**
 * Reading the JSON Schema a control-plane descriptor advertises.
 *
 * The descriptor's schema is derived in internal/app/controlplane/schema.go
 * from the Go type of the document itself, so it is structure only: keys,
 * types, and the `format`/`title`/`doc` annotations a field declares in its own
 * tags. It carries no prose of its own -- field descriptions have one home in
 * internal/webui/config_schema.go -- so everything here treats a missing
 * annotation as absent rather than inventing one.
 *
 * The live editor is value-driven (it walks the document it was given), so this
 * is metadata layered onto fields that exist, never a field list. A document
 * whose schema describes no properties renders exactly as it did before.
 */

/** One node of the derived schema. Only what the derivation emits is read. */
export interface SchemaProperty {
  type?: string;
  format?: string;
  title?: string;
  description?: string;
  properties?: Record<string, SchemaProperty>;
  items?: SchemaProperty;
  additionalProperties?: SchemaProperty | boolean;
}

/**
 * parseSchema reads a descriptor's schema_json. A descriptor that carries none,
 * or carries one this reader cannot parse, reports null: an unreadable schema
 * costs a placeholder, never a field.
 */
export function parseSchema(
  json: string | null | undefined,
): SchemaProperty | null {
  if (!json) return null;
  try {
    const parsed: unknown = JSON.parse(json);
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed))
      return null;
    return parsed as SchemaProperty;
  } catch {
    return null;
  }
}

function isKeyed(property: SchemaProperty): boolean {
  return (
    property.type === "object" &&
    !property.properties &&
    typeof property.additionalProperties === "object"
  );
}

/**
 * schemaAt resolves the property that describes one editor path.
 *
 * Paths are the editor's own (`repositories.0.owner`): dotted, with an array
 * item addressed by its index. `rootPath` is stripped first, because the
 * editor's path starts at the document rather than inside it -- the schema
 * describes the document, not a field of it.
 */
export function schemaAt(
  schema: SchemaProperty | null,
  path: string,
  rootPath = "",
): SchemaProperty | null {
  if (!schema) return null;
  const root = rootPath.split(".").filter(Boolean);
  let segments = path.split(".").filter(Boolean);
  if (
    root.length &&
    root.every((segment, index) => segments[index] === segment)
  ) {
    segments = segments.slice(root.length);
  }

  let current: SchemaProperty | null = schema;
  for (const segment of segments) {
    if (!current) return null;
    if (/^\d+$/.test(segment)) {
      current = current.items ?? null;
      continue;
    }
    const named = current.properties?.[segment];
    if (named) {
      current = named;
      continue;
    }
    // A keyed collection's keys belong to the operator, so the value's
    // schema describes whatever key they typed.
    current =
      typeof current.additionalProperties === "object"
        ? current.additionalProperties
        : null;
  }
  return current;
}

/** fieldTitle is the schema's own label for a key, or the dashed-key fallback
 * the editor has always used. */
export function fieldTitle(
  key: string,
  property: SchemaProperty | null | undefined,
): string {
  if (property?.title) return property.title;
  return key
    .replaceAll("_", " ")
    .replace(/^./, (character) => character.toUpperCase());
}

/**
 * fieldPlaceholder offers an example only where the schema declares a format
 * the field's value must match. A duration column is a string to the document
 * and a duration to the operator, and the example is what says so.
 */
export function fieldPlaceholder(
  property: SchemaProperty | null | undefined,
): string | undefined {
  if (property?.format === "duration") return "1m0s";
  if (property?.format === "date-time") return "2026-01-01T00:00:00Z";
  return undefined;
}

/** fieldHint is the schema's own prose for a field, empty when it has none. */
export function fieldHint(property: SchemaProperty | null | undefined): string {
  return property?.description ?? "";
}

/** isKeyedCollection reports a document whose keys are the operator's: a map
 * with no fixed properties, which the schema marks with additionalProperties. */
export function isKeyedCollection(
  property: SchemaProperty | null | undefined,
): boolean {
  return Boolean(property) && isKeyed(property as SchemaProperty);
}

/**
 * blankFromSchema synthesises a new row from the schema rather than from the
 * path that happens to hold it.
 *
 * The editor used to carry a literal template per array path -- a repository
 * skeleton, an MCP server skeleton -- which went stale the moment the document
 * was reshaped, and said nothing about the fields it did not know. A blank from
 * the schema cannot be stale, because the schema is derived from the document.
 * What it loses is the seeded convenience value (a default branch name, say):
 * a schema that wants those emits a default, which is the same single source of
 * truth this reader exists to honour.
 */
export function blankFromSchema(
  property: SchemaProperty | null | undefined,
): unknown {
  if (!property) return "";
  if (property.type === "object") {
    const blank: Record<string, unknown> = {};
    for (const [key, child] of Object.entries(property.properties ?? {})) {
      blank[key] = blankFromSchema(child);
    }
    return blank;
  }
  if (property.type === "array") return [];
  if (property.type === "boolean") return false;
  if (property.type === "integer" || property.type === "number") return 0;
  return "";
}
