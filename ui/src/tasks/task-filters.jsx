import { attentionStatusIds, statusIds } from "../base/task-meta.jsx";

// The vocabulary is the server catalog, not a hand-synced copy: the "needs
// you" grouping and the set of known statuses come straight from task-meta so
// a status added on the backend shows up here without a frontend change.
// "needs_you" is a UI pseudo-status (work waiting on a human), so it is
// prepended rather than stored in the catalog.
//
// Both are read at call time rather than captured at import. The catalog
// arrives after this module loads, so module-level constants here would freeze
// the defaults for the life of the process -- which is what they did, leaving
// the task filter unable to see a served status no matter when it landed.
function taskStatuses() {
  return new Set(["needs_you", ...statusIds()]);
}

export function initialTaskFilter(params) {
  const requested = params?.get?.("status") || "";
  return taskStatuses().has(requested) ? requested : "";
}

export function taskMatchesStatus(task, status) {
  if (!status) return true;
  if (status === "needs_you") return attentionStatusIds().has(task.status);
  return task.status === status;
}
