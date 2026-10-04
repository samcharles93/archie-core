import { isMap, isSeq, parseDocument } from "yaml";

/**
 * Canvas edits as edits to the workflow's YAML document. The text stays the
 * one source of truth, so the canvas and the YAML tab cannot disagree, and
 * comments and layout the operator wrote survive an edit made on the canvas.
 */

/** Where a step lives in the document: ["steps", 2], or
 * ["steps", 1, "parallel", "docs", 0] for a step inside a branch. */
export type StepPath = (string | number)[];

export type StepRecord = Record<string, unknown>;

/** A step as plain data, or undefined when the path names none. */
export function stepAt(source: string, path: StepPath): StepRecord | undefined {
  const node = parseDocument(source).getIn(path, true);
  if (!isMap(node)) return undefined;
  return node.toJSON() as StepRecord;
}

/** Replaces one step with a new value. Unset keys are dropped, so a cleared
 * field leaves no empty key behind. */
export function replaceStep(source: string, path: StepPath, step: StepRecord): string {
  const document = parseDocument(source);
  document.setIn(path, document.createNode(prune(step)));
  return document.toString();
}

/** Inserts a new step of the given type after the top-level step at index
 * (-1 inserts first; past the end appends). Returns the new source and the
 * inserted step's path. */
export function insertStep(source: string, after: number, type: string): { source: string; path: StepPath } {
  const document = parseDocument(source);
  let steps = document.get("steps", true);
  if (!isSeq(steps)) {
    document.set("steps", document.createNode([]));
    steps = document.get("steps", true);
  }
  if (!isSeq(steps)) return { source, path: [] };
  const index = Math.min(Math.max(after + 1, 0), steps.items.length);
  steps.items.splice(index, 0, document.createNode({ type }));
  return { source: document.toString(), path: ["steps", index] };
}

/** Copies a step to just after itself. The copy drops the id, which must stay
 * unique. Returns the new source and the copy's path. */
export function duplicateStep(source: string, path: StepPath): { source: string; path: StepPath } {
  const document = parseDocument(source);
  const list = document.getIn(path.slice(0, -1), true);
  const index = path.at(-1);
  if (!isSeq(list) || typeof index !== "number") return { source, path };
  const copy = document.createNode(document.getIn(path));
  if (isMap(copy)) copy.delete("id");
  list.items.splice(index + 1, 0, copy);
  return { source: document.toString(), path: [...path.slice(0, -1), index + 1] };
}

/** Removes a step. A branch left empty is removed with it. */
export function deleteStep(source: string, path: StepPath): string {
  const document = parseDocument(source);
  document.deleteIn(path);
  const branch = path.slice(0, -1);
  if (branch.length > 2) {
    const remaining = document.getIn(branch, true);
    if (isSeq(remaining) && remaining.items.length === 0) document.deleteIn(branch);
  }
  return document.toString();
}

/** Moves a step one place up (-1) or down (+1) within its own list. Returns
 * the new source and the step's new path. */
export function moveStep(source: string, path: StepPath, delta: -1 | 1): { source: string; path: StepPath } {
  const document = parseDocument(source);
  const list = document.getIn(path.slice(0, -1), true);
  const index = path.at(-1);
  if (!isSeq(list) || typeof index !== "number") return { source, path };
  const target = index + delta;
  if (target < 0 || target >= list.items.length) return { source, path };
  const [item] = list.items.splice(index, 1);
  list.items.splice(target, 0, item);
  return { source: document.toString(), path: [...path.slice(0, -1), target] };
}

/** The ids every step before the given one declares, which are the steps its
 * settings may reference. Inside a branch, sibling branches are not visible. */
export function earlierStepIDs(source: string, path: StepPath): string[] {
  const value = parseDocument(source).toJS() as { steps?: unknown[] } | null;
  const steps = Array.isArray(value?.steps) ? value.steps : [];
  const top = typeof path[1] === "number" ? path[1] : steps.length;
  const ids: string[] = [];
  const collect = (step: unknown) => {
    if (typeof step !== "object" || step === null) return;
    const record = step as StepRecord;
    if (typeof record.id === "string") ids.push(record.id);
    if (typeof record.parallel === "object" && record.parallel !== null)
      for (const branch of Object.values(record.parallel as Record<string, unknown[]>))
        if (Array.isArray(branch)) branch.forEach(collect);
  };
  steps.slice(0, top).forEach(collect);
  if (path.length === 5) {
    const branch = (steps[top] as StepRecord | undefined)?.parallel as Record<string, unknown[]> | undefined;
    const own = branch?.[path[3] as string];
    if (Array.isArray(own)) own.slice(0, path[4] as number).forEach(collect);
  }
  return ids;
}

function prune(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(prune);
  if (typeof value !== "object" || value === null) return value;
  const out: Record<string, unknown> = {};
  for (const [key, child] of Object.entries(value)) {
    if (child === undefined || child === "" || child === null) continue;
    const pruned = prune(child);
    if (typeof pruned === "object" && pruned !== null && !Array.isArray(pruned) && Object.keys(pruned).length === 0) continue;
    out[key] = pruned;
  }
  return out;
}
