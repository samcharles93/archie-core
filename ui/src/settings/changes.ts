/** One changed leaf of a settings document. */
export interface Change {
  path: string;
  before: unknown;
  after: unknown;
}

const isRecord = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v);

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

/**
 * diffValues lists what changed between two settings documents, one entry per
 * changed leaf. Objects recurse by key and arrays of objects by index; an
 * array of plain values is one leaf, because its entries have no identity of
 * their own. A row added or removed is a single change.
 */
export function diffValues(before: unknown, after: unknown, path = ""): Change[] {
  if (same(before, after)) return [];
  const at = (key: string | number) => (path ? `${path}.${key}` : String(key));

  if (isRecord(before) && isRecord(after)) {
    const keys = [...new Set([...Object.keys(before), ...Object.keys(after)])];
    return keys.flatMap((key) => diffValues(before[key], after[key], at(key)));
  }
  const rows = (v: unknown) => Array.isArray(v) && v.some(isRecord);
  if (Array.isArray(before) && Array.isArray(after) && (rows(before) || rows(after))) {
    const length = Math.max(before.length, after.length);
    return Array.from({ length }, (_, i) => diffValues(before[i], after[i], at(i))).flat();
  }
  return [{ path, before, after }];
}

/**
 * saveInOrder saves each kind in turn and stops at the first failure: saves
 * are not atomic across kinds, so the ones after a failure stay unsaved
 * rather than landing on top of a half-applied set.
 */
export async function saveInOrder(
  kinds: string[],
  save: (kind: string) => Promise<boolean>,
): Promise<{ saved: string[]; failed: string | undefined; untouched: string[] }> {
  const saved: string[] = [];
  for (const [i, kind] of kinds.entries()) {
    if (!(await save(kind)))
      return { saved, failed: kind, untouched: kinds.slice(i + 1) };
    saved.push(kind);
  }
  return { saved, failed: undefined, untouched: [] };
}

/** formatValue renders one side of a change for the review diff. */
export function formatValue(value: unknown): string {
  if (value === undefined) return "unset";
  if (typeof value === "string") return value === "" ? '""' : value;
  return JSON.stringify(value);
}
