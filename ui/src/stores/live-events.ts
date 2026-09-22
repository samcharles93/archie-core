export type LiveResource = "tasks" | "captures" | "curators" | "skills" | "updates";

export interface LiveEvent {
  id?: number;
  kind?: string;
  type?: string;
  task_id?: number | string;
  repo?: string;
  issue?: number | string;
  at?: string | number;
  detail?: string;
  data?: Record<string, unknown>;
}

const taskKinds = new Set([
  "stage_start",
  "stage_finish",
  "agent_finish",
  "tool_call",
  "parked",
  "outcome",
  "pr_merged",
  "pr_rejected",
  "human_approved",
  "human_rejected",
  "agent_approved",
  "agent_rejected",
  "changes_captured",
  "config_captured",
  "work_request_submitted",
]);

/** Map a durable backend event to the read projections it invalidates. */
export function resourcesForEvent(raw: unknown): LiveResource[] {
  const event = raw as LiveEvent | null;
  const kind = event?.kind ?? "";
  const resources = new Set<LiveResource>();
  if (event?.task_id || kind.startsWith("task_") || taskKinds.has(kind)) resources.add("tasks");
  if (kind === "capture") resources.add("captures");
  if (kind.startsWith("curator_")) resources.add("curators");
  if (kind === "curator_action") resources.add("skills");
  if (kind === "update_report") resources.add("updates");
  return [...resources];
}
