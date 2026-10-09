import { isMap, isScalar, isSeq, parseDocument } from "yaml";

/**
 * Canvas edits as edits to the workflow's YAML document. The text stays the
 * one source of truth, so the canvas and the YAML tab cannot disagree, and
 * comments and layout the operator wrote survive an edit made on the canvas.
 */

/** Where a step lives in the document: ["steps", 2], or
 * ["steps", 1, "parallel", "docs", 0] for a step inside a branch, or
 * ["steps", 1, "switch", "cases", "false", 0] for a step inside a case. */
export type StepPath = (string | number)[];

export type StepRecord = Record<string, unknown>;

/** A step's nested step lists: parallel branches, which all run, or switch
 * cases, of which one runs. base is where the lists sit under the step. */
export interface Branching {
  kind: "parallel" | "switch";
  base: string[];
  on?: string;
  branches: [string, unknown[]][];
}

const isRecord = (value: unknown): value is StepRecord => typeof value === "object" && value !== null && !Array.isArray(value);
const lists = (value: unknown): [string, unknown[]][] =>
  isRecord(value) ? Object.entries(value).map(([name, steps]) => [name, Array.isArray(steps) ? steps : []]) : [];

export function branchingOf(step: unknown): Branching | undefined {
  if (!isRecord(step)) return undefined;
  if (isRecord(step.parallel)) return { kind: "parallel", base: ["parallel"], branches: lists(step.parallel) };
  if (isRecord(step.switch))
    return { kind: "switch", base: ["switch", "cases"], on: typeof step.switch.on === "string" ? step.switch.on : "", branches: lists(step.switch.cases) };
  return undefined;
}

/** Parallel branches share a worktree, so only read-only agents and calls run
 * there. A switch case runs alone and takes any step. */
export function inParallel(path: StepPath): boolean {
  return path.slice(2).includes("parallel");
}

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

/** Inserts after a step path; index -1 inserts first in that list. */
export function insertStep(source: string, after: StepPath, type: string): { source: string; path: StepPath } {
  const document = parseDocument(source);
  const list = after.slice(0, -1);
  const inBranch = inParallel(list);
  if (inBranch && !["agent.run", "workflow.call"].includes(type)) throw new Error("Branches allow only read-only agents and workflow calls.");
  let steps = document.getIn(list, true);
  if (!isSeq(steps)) {
    document.setIn(list, document.createNode([]));
    steps = document.getIn(list, true);
  }
  if (!isSeq(steps)) return { source, path: [] };
  const index = Math.min(Math.max(Number(after.at(-1)) + 1, 0), steps.items.length);
  const step = type === "parallel" ? { parallel: { "branch-1": [], "branch-2": [] } }
    : type === "switch" ? { switch: { on: "", cases: { "true": [], default: [] } } } : { type, ...(inBranch && type === "agent.run" ? { settings: { read_only: true } } : {}) };
  steps.items.splice(index, 0, document.createNode(step));
  return { source: document.toString(), path: [...list, index] };
}

function checkBranchName(name: string, kind: Branching["kind"]): void {
  if (kind === "switch") {
    if (!name || name.includes("/")) throw new Error("A case is the value it matches, without a slash.");
    return;
  }
  if (!/^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$/.test(name)) throw new Error("Use a lowercase branch name with letters, digits, dots or dashes.");
}

/** The map of a step's branches or cases, with what kind of step holds it. */
function branchMap(document: ReturnType<typeof parseDocument>, path: StepPath) {
  const step = document.getIn(path, true);
  const branching = branchingOf(isMap(step) ? step.toJSON() : undefined);
  const branches = branching ? document.getIn([...path, ...branching.base], true) : undefined;
  if (!branching || !isMap(branches)) throw new Error("This step has no branches.");
  return { kind: branching.kind, branches };
}

export function addBranch(source: string, path: StepPath, name: string): string {
  const document = parseDocument(source);
  const { kind, branches } = branchMap(document, path);
  checkBranchName(name, kind);
  if (branches.has(name)) throw new Error(`Branch ${name} already exists.`);
  branches.set(name, document.createNode([]));
  return document.toString();
}

export function renameBranch(source: string, path: StepPath, name: string, next: string): string {
  const document = parseDocument(source);
  const { kind, branches } = branchMap(document, path);
  checkBranchName(next, kind);
  if (name !== next && branches.has(next)) throw new Error(`Branch ${next} already exists.`);
  const pair = branches.items.find((pair) => isScalar(pair.key) && pair.key.value === name);
  if (!pair || !isScalar(pair.key)) throw new Error(`Branch ${name} does not exist.`);
  pair.key.value = next;
  return document.toString();
}

export function deleteBranch(source: string, path: StepPath, name: string): string {
  const document = parseDocument(source);
  const { kind, branches } = branchMap(document, path);
  if (kind === "parallel" && branches.items.length <= 2) throw new Error("Parallel needs at least two branches.");
  if (branches.items.length <= 1) throw new Error("A switch needs at least one case.");
  branches.delete(name);
  return document.toString();
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

/** Removes a step, leaving an empty branch available for authoring. */
export function deleteStep(source: string, path: StepPath): string {
  const document = parseDocument(source);
  document.deleteIn(path);
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
    for (const [, branch] of branchingOf(record)?.branches ?? []) branch.forEach(collect);
  };
  steps.slice(0, top).forEach(collect);
  if (path.length > 2) {
    const name = path.at(-2) as string;
    const own = branchingOf(steps[top])?.branches.find(([branch]) => branch === name)?.[1];
    own?.slice(0, path.at(-1) as number).forEach(collect);
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

/** A top-level workflow field (repository, inputs, profile, ...) as plain data. */
export function workflowField(source: string, key: string): unknown {
  const value = parseDocument(source).get(key, true);
  return value && typeof value === "object" && "toJSON" in value ? (value as { toJSON(): unknown }).toJSON() : parseDocument(source).get(key);
}

/** Sets a top-level workflow field, or removes it when the value is empty. */
export function setWorkflowField(source: string, key: string, value: unknown): string {
  const document = parseDocument(source);
  const pruned = (prune({ [key]: value }) as Record<string, unknown>)[key];
  if (pruned === undefined) document.delete(key);
  else document.set(key, document.createNode(pruned));
  return document.toString();
}
