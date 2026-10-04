import { parseDocument } from "yaml";

/**
 * A workflow definition laid out as a graph: a start node, each step as a
 * node in run order, parallel branches side by side, and a dashed edge from
 * every step whose result a later step reads.
 */

export interface StepNodeData {
  kind: "start" | "step";
  /** The step's id, or its 1-based position when it has none. */
  key: string;
  title: string;
  type: string;
  detail: string;
  when?: string;
  retry?: number;
  continues?: boolean;
  branch?: string;
}

export interface GraphNode {
  id: string;
  type: "step";
  position: { x: number; y: number };
  data: StepNodeData;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  /** "flow" is run order; "data" is a reference to an earlier result. */
  kind: "flow" | "data";
  label?: string;
  sourceHandle?: string;
  targetHandle?: string;
  offset?: number;
}

export interface WorkflowGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

const ROW = 120;
const COLUMN = 280;

const TYPE_TITLES: Record<string, string> = {
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

  const start = "start";
  nodes.push({
    id: start,
    type: "step",
    position: { x: 0, y: 0 },
    data: { kind: "start", key: "start", title: "Start", type: "", detail: "an event or a task starts the run" },
  });

  let previous = [start];
  let row = 1;
  const addStep = (step: unknown, key: string, x: number, y: number, branch?: string): string => {
    const record = isMapping(step) ? step : {};
    const type = typeof record.type === "string" ? record.type : "";
    const settings = isMapping(record.settings) ? record.settings : {};
    const stepID = typeof record.id === "string" ? record.id : "";
    const id = `step-${key}`;
    nodes.push({
      id,
      type: "step",
      position: { x, y },
      data: {
        kind: "step",
        key: stepID || key,
        title: stepID || stepTitle(type),
        type,
        detail: stepDetail(type, settings),
        when: typeof record.when === "string" ? record.when : undefined,
        retry: isMapping(record.retry) && typeof record.retry.attempts === "number" ? record.retry.attempts : undefined,
        continues: record.on_failure === "continue",
        branch,
      },
    });
    if (stepID) byStepID.set(stepID, id);
    for (const ref of referencedSteps(record.settings, record.when)) pendingData.push({ from: ref, to: id });
    return id;
  };
  const link = (from: string[], to: string) => {
    for (const source of from) edges.push({ id: `flow-${source}-${to}`, source, target: to, kind: "flow" });
  };

  for (const [i, step] of steps.entries()) {
    if (isMapping(step) && isMapping(step.parallel)) {
      const branches = Object.entries(step.parallel);
      const ends: string[] = [];
      let depth = 0;
      branches.forEach(([name, branchSteps], column) => {
        const x = (column - (branches.length - 1) / 2) * COLUMN;
        let tail = previous;
        (Array.isArray(branchSteps) ? branchSteps : []).forEach((branchStep, j) => {
          const id = addStep(branchStep, `${i + 1}-${name}-${j + 1}`, x, (row + j) * ROW, name);
          link(tail, id);
          tail = [id];
          depth = Math.max(depth, j + 1);
        });
        ends.push(...tail);
      });
      previous = ends;
      row += Math.max(depth, 1);
      continue;
    }
    const id = addStep(step, String(i + 1), 0, row * ROW);
    link(previous, id);
    previous = [id];
    row++;
  }

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
