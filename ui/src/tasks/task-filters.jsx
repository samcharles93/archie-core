import { attentionStatusIds, statusIds } from "../base/task-meta.jsx";

// The vocabulary is the server catalog, not a hand-synced copy: the "needs
// you" grouping and the set of known statuses come straight from task-meta so
// a status added on the backend shows up here without a frontend change.
// "needs_you" is a UI pseudo-status (work waiting on a human), so it is
// prepended rather than stored in the catalog.
//
// Both sets are computed per call rather than at module load: the catalog is
// replaced when the served /api/task-meta payload arrives, and a snapshot
// captured at import time would never see it.
export function initialTaskFilter(params) {
  const requested = params?.get?.("status") || "";
  const known = new Set(["needs_you", ...statusIds()]);
  return known.has(requested) ? requested : "";
}

export function taskMatchesStatus(task, status) {
  if (!status) return true;
  if (status === "needs_you") return attentionStatusIds().has(task.status);
  return task.status === status;
}
