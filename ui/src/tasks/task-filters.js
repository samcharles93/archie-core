const ATTENTION_STATUSES = new Set(["waiting_human", "parked"]);
const TASK_STATUSES = new Set([
  "needs_you",
  "queued",
  "running",
  "waiting_human",
  "pr_open",
  "merged",
  "parked",
  "dead",
  "rejected",
  "closed_wont_do",
]);

export function initialTaskFilter(params) {
  const requested = params?.get?.("status") || "";
  return TASK_STATUSES.has(requested) ? requested : "";
}

export function taskMatchesStatus(task, status) {
  if (!status) return true;
  if (status === "needs_you") return ATTENTION_STATUSES.has(task.status);
  return task.status === status;
}
