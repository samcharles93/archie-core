import { parseDocument } from "yaml";

import { branchingOf } from "./workflow-edit.ts";
import type { WorkflowTrigger } from "./workflow-triggers";

/**
 * A workflow definition laid out as a graph: a start node, each step as a
 * node in run order, parallel branches side by side, and a dashed edge from
 * every step whose result a later step reads.
 */

export interface StepNodeData {
  kind: "start" | "step";
  /** What the step means to the run: work, a gate that decides, a pause for a
   * person, or an exit that ends or hands off the run. */
  role?: StepRole;
  /** The top-level step's position in run order, as shown on the node. */
  step?: string;
  /** The step's id, or its 1-based position when it has none. */
  key: string;
  title: string;
  type: string;
  detail: string;
  /** The first line of what the step is told to do, if it says. */
  summary?: string;
  when?: string;
  retry?: number;
  continues?: boolean;
  branch?: string;
  /** Whether the branch is one of several that all run, or a switch case. */
  lane?: "parallel" | "switch";
  /** The name the engine records this step's run under, and which run of
   * that name it is: two unnamed repo.commit steps are commit 0 and 1. */
  stage?: string;
  occurrence?: number;
  /** The top-level stage this node resumes the run from. A branch step's own
   * recorded name is branch-qualified and cannot resume on its own, so it
   * names the parallel step it belongs to here. */
  resume?: string;
  run?: StageRun;
  /** Where the step lives in the YAML document, for editing it. */
  path?: (string | number)[];
  selected?: boolean;
  triggers?: WorkflowTrigger[];
  insertAfter?: (string | number)[];
}

export type StepRole = "work" | "decision" | "waiting" | "exit";

function stepRole(type: string): StepRole {
  if (type.startsWith("gate.") || type === "switch") return "decision";
  if (type === "human.approve") return "waiting";
  if (type === "workflow.finish" || type === "workflow.handoff") return "exit";
  return "work";
}

/** One recorded run of a step, from the task's attempts view. */
export interface AgentRun {
  id: number;
  name: string;
  status: string;
  detail: string;
  tokens_used: number;
  started_at: string;
  finished_at?: string;
}

export interface StageRun {
  id?: number;
  started_at?: string;
  finished_at?: string;
  agents?: AgentRun[];
  name: string;
  status: "ok" | "failed" | "interrupted" | "running" | "skipped" | "unknown" | string;
  duration_ms?: number;
  error?: string;
}

/** Puts each recorded stage run on the node it belongs to. */
export function withRuns(graph: WorkflowGraph, stages: StageRun[]): WorkflowGraph {
  const byName = new Map<string, StageRun[]>();
  for (const stage of stages) byName.set(stage.name, [...(byName.get(stage.name) ?? []), stage]);
  return {
    edges: graph.edges,
    nodes: graph.nodes.map((node) => {
      const { stage, occurrence } = node.data;
      const run = stage === undefined ? undefined : byName.get(stage)?.[occurrence ?? 0];
      return run ? { ...node, data: { ...node.data, run } } : node;
    }),
  };
}

/** How a parked run can restart at a step: the top-level step it stopped at
 * resumes, earlier ones re-run. Later steps, and runs that are not parked,
 * offer nothing. */
export interface Restart {
  label: "Resume from here" | "Re-run from here" | "Add an id to resume here";
  from: string;
  /** The server would refuse this resume point: the step needs an id. */
  blocked?: boolean;
}

export function restartAt(graph: WorkflowGraph, path: (string | number)[]): Restart | null {
  const top = (data: StepNodeData) => (data.path && data.path.length >= 2 ? Number(data.path[1]) : -1);
  const node = graph.nodes.find((candidate) => JSON.stringify(candidate.data.path) === JSON.stringify(path))?.data;
  const from = node?.resume ?? node?.stage;
  if (!node || !from) return null;
  const stopped = Math.max(-1, ...graph.nodes.filter((candidate) => candidate.data.run).map((candidate) => top(candidate.data)));
  const at = top(node);
  if (at < 0 || at > stopped) return null;
  // The server resumes by stage name, so it refuses a step whose name, or the
  // name of the step before it, runs more than once.
  const names = graph.nodes
    .filter((candidate) => candidate.data.path?.length === 2)
    .sort((a, b) => top(a.data) - top(b.data))
    .map((candidate) => candidate.data.resume ?? candidate.data.stage ?? "");
  const repeated = (name: string | undefined) => !!name && names.filter((n) => n === name).length > 1;
  if (repeated(from) || (at > 0 && repeated(names[at - 1])))
    return { label: "Add an id to resume here", from, blocked: true };
  return { label: at === stopped ? "Resume from here" : "Re-run from here", from };
}

export interface GraphNode {
  id: string;
  type: "step" | "add" | "lane";
  /** A lane's size, drawn behind its steps. */
  style?: { width: string; height: string };
  zIndex?: number;
  position: { x: number; y: number };
  data: StepNodeData;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  /** "flow" is run order; "data" is a reference to an earlier result. */
  kind: "flow" | "data";
  /** A flow edge that skips past a switch none of whose cases matched. */
  otherwise?: boolean;
  label?: string;
  sourceHandle?: string;
  targetHandle?: string;
  offset?: number;
  /** The predecessor path for insertion; index -1 inserts first in a list. */
  insertAfter?: (string | number)[];
}

export interface WorkflowGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

const ROW = 140;
const COLUMN = 280;

const TYPE_TITLES: Record<string, string> = {
  parallel: "Parallel",
  switch: "Switch",
  "agent.run": "Agent",
  "command.run": "Run commands",
  "repo.prepare": "Prepare worktree",
  "repo.commit": "Commit",
  "repo.open-pr": "Open pull request",
  "gate.repository": "Repository gate",
  "gate.diff-size": "Diff size gate",
  "gate.diff-rules": "Diff rules gate",
  "forge.comment": "Comment",
  "forge.close-issue": "Close issue",
  "human.approve": "Human approval",
  "workflow.call": "Call workflow",
  "workflow.handoff": "Hand off",
  "workflow.finish": "Finish",
  "review.pr": "Review pull request",
  "review.start-round": "Start review round",
  "review.reply": "Reply to review",
};

/** A step type's display name: a known one's title, else the type itself. */
export function stepTitle(type: string): string {
  return TYPE_TITLES[type] ?? type;
}

const REFERENCE = /\{\{\s*steps\.([A-Za-z0-9_-]+)\.[^}]*\}\}/g;

type Mapping = Record<string, unknown>;

function isMapping(value: unknown): value is Mapping {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** One line saying what a step does, from the settings that matter most. */
function stepDetail(type: string, settings: Mapping): string {
  const pick = (key: string) => (typeof settings[key] === "string" ? (settings[key] as string) : "");
  switch (type) {
    case "agent.run": {
      const role = pick("role") || "builder";
      const mode = settings.read_only ? "read-only" : pick("gate") ? `gated: ${pick("gate")}` : "";
      return [role, mode].filter(Boolean).join(" · ");
    }
    case "repo.commit":
      return [pick("message"), settings.push ? "push" : ""].filter(Boolean).join(" · ");
    case "workflow.call":
    case "workflow.handoff":
      return pick("workflow");
    case "workflow.finish":
      return pick("status") || "completed";
    case "human.approve":
      return pick("then") ? `then ${pick("then")}` : "";
    case "gate.repository":
      return pick("expect") === "test-failure" ? "tests must fail" : settings.repair ? "repair if red" : "";
  }
  return "";
}

/** The first line of a step's main text setting, references shown by name. */
function stepSummary(settings: Mapping): string | undefined {
  for (const key of ["mission", "message", "body", "plan", "detail"]) {
    const value = settings[key];
    if (typeof value !== "string" || !value.trim()) continue;
    return value
      .replace(/\{\{\s*([^}]+?)\s*\}\}/g, (_, ref: string) => `‹${ref.replace(/^steps\./, "").replace(/\.result\b/, "")}›`)
      .split("\n")
      .find((line) => line.trim())
      ?.trim();
  }
  return undefined;
}

/** Every step id a step's settings reference. */
function referencedSteps(settings: unknown, when: unknown): string[] {
  const text = JSON.stringify(settings ?? {}) + " " + (typeof when === "string" ? `{{ ${when.replace(/^!/, "")} }}` : "");
  return [...new Set([...text.matchAll(REFERENCE)].map((match) => match[1]))];
}

export function workflowGraph(source: string): WorkflowGraph {
  const value: unknown = parseDocument(source).toJS();
  const steps = isMapping(value) && Array.isArray(value.steps) ? value.steps : [];
  const nodes: GraphNode[] = [];
  const edges: GraphEdge[] = [];
  const byStepID = new Map<string, string>();
  const pendingData: { from: string; to: string }[] = [];
  const otherwise = new Set<string>();

  const start = "start";
  nodes.push({
    id: start,
    type: "step",
    position: { x: 0, y: 0 },
    data: { kind: "start", key: "start", title: "Starts when", type: "", detail: "" },
  });

  let previous = [start];
  let row = 1;
  const seen = new Map<string, number>();
  const occurrenceOf = (stage: string): number => {
    const count = seen.get(stage) ?? 0;
    seen.set(stage, count + 1);
    return count;
  };
  const addStep = (
    step: unknown,
    key: string,
    x: number,
    y: number,
    branch?: string,
    resume?: string,
    path?: (string | number)[],
    lane?: "parallel" | "switch",
  ): string => {
    const record = isMapping(step) ? step : {};
    const branching = branchingOf(record);
    const type = branching?.kind ?? (typeof record.type === "string" ? record.type : "");
    const settings = isMapping(record.settings) ? record.settings : {};
    const stepID = typeof record.id === "string" ? record.id : "";
    const id = `step-${key}`;
    // A branch step's row is named after its branch, so two branches running
    // the same step are two rows rather than one shared run.
    const recordName = branch ? `${branch}/${stepID || type}` : stepID || type;
    nodes.push({
      id,
      type: "step",
      position: { x, y },
      data: {
        kind: "step",
        key: stepID || key,
        title: stepID || stepTitle(type),
        type,
        role: stepRole(type),
        step: path?.length === 2 ? String(Number(path[1]) + 1).padStart(2, "0") : undefined,
        detail: branching?.on ? `on ${branching.on.replace(/^steps\./, "")}` : stepDetail(type, settings),
        summary: stepSummary(settings),
        when: typeof record.when === "string" ? record.when : undefined,
        retry: isMapping(record.retry) && typeof record.retry.attempts === "number" ? record.retry.attempts : undefined,
        continues: record.on_failure === "continue",
        branch,
        lane,
        path,
        stage: recordName,
        occurrence: occurrenceOf(recordName),
        ...(resume ? { resume } : {}),
      },
    });
    if (stepID) byStepID.set(stepID, id);
    const on = branching?.on ? `{{ ${branching.on} }}` : "";
    for (const ref of referencedSteps([record.settings, on], record.when)) pendingData.push({ from: ref, to: id });
    return id;
  };
  const link = (from: string[], to: string, insertAfter?: (string | number)[]) => {
    // Several edges joining into one step share one insertion point.
    from.forEach((source, n) =>
      edges.push({
        id: `flow-${source}-${to}`,
        source,
        target: to,
        kind: "flow",
        ...(otherwise.has(source) ? { otherwise: true } : {}),
        insertAfter: n === from.length - 1 ? insertAfter : undefined,
      }),
    );
  };

  for (const [i, step] of steps.entries()) {
    const branching = branchingOf(step);
    if (branching) {
      const { kind, base, branches } = branching;
      const parent = addStep(step, String(i + 1), 0, row * ROW, undefined, undefined, ["steps", i]);
      link(previous, parent, ["steps", i - 1]);
      row++;
      // Each branch step records its own row under the parallel or switch
      // step, so the canvas lights every branch node from its own run.
      const parentName = isMapping(step) && typeof step.id === "string" ? step.id : kind;
      // A switch with no default case lets the run go straight on when no
      // case matches; that way out is drawn as a lane of its own.
      const passes = kind === "switch" && !branches.some(([name]) => name === "default");
      const lanes = branches.length + (passes ? 1 : 0);
      const ends: string[] = [];
      let depth = 0;
      branches.forEach(([name, branchSteps], column) => {
        const x = (column - (lanes - 1) / 2) * COLUMN;
        let tail = [parent];
        const path = ["steps", i, ...base, name];
        branchSteps.forEach((branchStep, j) => {
          const id = addStep(branchStep, `${i + 1}-${name}-${j + 1}`, x, (row + j) * ROW, name, parentName, [...path, j], kind);
          link(tail, id, [...path, j - 1]);
          tail = [id];
        });
        depth = Math.max(depth, branchSteps.length);
        const end = `add-${i}-${name}`;
        nodes.push({ id: end, type: "add", position: { x: x + 104, y: (row + branchSteps.length) * ROW }, data: { kind: "step", key: end, title: "", type: "", detail: "", branch: name, lane: kind, insertAfter: [...path, branchSteps.length - 1] } });
        link(tail, end);
        ends.push(end);
      });
      const height = (Math.max(depth, 1) + 1) * ROW - 24;
      branches.forEach(([name], column) => {
        const x = (column - (lanes - 1) / 2) * COLUMN;
        nodes.push({
          id: `lane-${i}-${name}`,
          type: "lane",
          position: { x: x - 12, y: row * ROW - 40 },
          style: { width: `${COLUMN - 16}px`, height: `${height}px` },
          zIndex: -1,
          data: { kind: "step", key: name, title: name, type: "", detail: "", lane: kind },
        });
      });
      if (passes) {
        // The run's way past an unmatched switch: an edge from the switch to
        // the step after it, drawn down the last column.
        ends.push(parent);
        otherwise.add(parent);
      }
      previous = ends;
      row += Math.max(depth, 1) + 1;
      continue;
    }
    const id = addStep(step, String(i + 1), 0, row * ROW, undefined, undefined, ["steps", i]);
    link(previous, id, ["steps", i - 1]);
    previous = [id];
    row++;
  }

  // The end of the run, where a new last step is added.
  nodes.push({
    id: "add-end",
    type: "add",
    position: { x: 104, y: row * ROW - 24 },
    data: { kind: "step", key: "add", title: "", type: "", detail: "", insertAfter: ["steps", steps.length - 1] },
  });
  link(previous, "add-end");

  for (const { from, to } of pendingData) {
    const source = byStepID.get(from);
    if (!source) continue;
    // A data edge that only restates the run-order edge adds nothing.
    if (edges.some((edge) => edge.kind === "flow" && edge.source === source && edge.target === to)) continue;
    edges.push({
      id: `data-${source}-${to}`,
      source,
      target: to,
      kind: "data",
      label: from,
      // Each data edge loops a little further out, so several from one step
      // stay apart instead of drawing one line.
      offset: 24 + 14 * edges.filter((edge) => edge.kind === "data").length,
      sourceHandle: "data-out",
      targetHandle: "data-in",
    });
  }
  return { nodes, edges };
}
