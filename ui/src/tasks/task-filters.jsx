import { attentionStatusIds, statusIds } from "../base/task-meta.jsx";

// The vocabulary is the server catalog, not a hand-synced copy: the "needs
// you" grouping and the set of known statuses come straight from task-meta so
// a status added on the backend shows up here without a frontend change.
// "needs_you" is a UI pseudo-status (work waiting on a human), so it is
// prepended rather than stored in the catalog.
const ATTENTION_STATUSES = attentionStatusIds();
const TASK_STATUSES = new Set(["needs_you", ...statusIds()]);

export function initialTaskFilter(params) {
  const requested = params?.get?.("status") || "";
  return TASK_STATUSES.has(requested) ? requested : "";
}

export function taskMatchesStatus(task, status) {
  if (!status) return true;
  if (status === "needs_you") return ATTENTION_STATUSES.has(task.status);
  return task.status === status;
}
