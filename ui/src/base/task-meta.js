// Central lifecycle vocabulary for the dashboard. The backend is the source of
// truth: /api/task-meta returns the statuses (label, pill severity, "needs you"
// grouping) and operator actions (label, button variant, confirm prompt). This
// module fetches that catalog and exposes the accessors the rest of the UI
// reads, so no surface keeps a hand-synced copy of the vocabulary.
//
// The defaults below are a freeze-dried snapshot that render immediately and
// keep working when archied is unreachable; loadTaskMeta() replaces them with
// the live catalog as soon as it arrives. They must match the backend catalog
// so drawing the page never flashes a stale label.
import { api } from "./api.js";

const DEFAULT_STATUSES = [
  { id: "queued", label: "Queued", kind: "idle" },
  { id: "running", label: "Working", kind: "info" },
  { id: "waiting_human", label: "Waiting for you", kind: "warn", needs_you: true },
  { id: "pr_open", label: "In review", kind: "ok" },
  { id: "merged", label: "Merged", kind: "ok" },
  { id: "parked", label: "Parked", kind: "warn", needs_you: true },
  { id: "dead", label: "Stopped (too many retries)", kind: "danger" },
  { id: "rejected", label: "Rejected", kind: "danger" },
  { id: "closed_wont_do", label: "Won't do", kind: "idle" },
];

const DEFAULT_ACTIONS = [
  { id: "cancel", label: "Cancel", kind: "quiet", confirm: `Cancel "{title}"? This closes the forge issue.` },
  { id: "stop", label: "Stop", kind: "primary", confirm: `Stop "{title}"? Recoverable work will remain parked.` },
  { id: "approve", label: "Approve", kind: "primary" },
  { id: "reject", label: "Reject", kind: "quiet", confirm: `Reject "{title}"? This closes the forge issue.` },
  { id: "retry", label: "Retry", kind: "primary" },
  { id: "abandon", label: "Abandon", kind: "quiet", confirm: `Abandon "{title}"? This closes the forge issue.` },
  { id: "archive", label: "Archive", kind: "quiet", confirm: `Archive the local record for "{title}"?` },
  { id: "open_pr", label: "Open PR", kind: "link" },
  { id: "open_issue", label: "Open issue", kind: "link" },
];

let statuses = [...DEFAULT_STATUSES];
let actions = [...DEFAULT_ACTIONS];
let statusById = new Map(statuses.map((s) => [s.id, s]));
let actionById = new Map(actions.map((a) => [a.id, a]));

function rebuildIndexes() {
  statusById = new Map(statuses.map((s) => [s.id, s]));
  actionById = new Map(actions.map((a) => [a.id, a]));
}

// loadTaskMeta upgrades the dashboard from the server catalog. It never throws:
// a failed fetch keeps the defaults so the UI still renders.
export async function loadTaskMeta() {
  try {
    const data = await api.taskMeta();
    if (Array.isArray(data?.statuses)) statuses = data.statuses;
    if (Array.isArray(data?.actions)) actions = data.actions;
    rebuildIndexes();
  } catch {
    // archied unreachable or not yet serving this route; keep the defaults.
  }
}

// statusLabel is the human-readable name for a lifecycle status.
export function statusLabel(id) {
  return statusById.get(id)?.label || id || "Unknown";
}

// statusKind is the pill severity token (idle, info, ok, warn, danger).
export function statusKind(id) {
  return statusById.get(id)?.kind || "idle";
}

// statusList returns every known lifecycle status in display order.
export function statusList() {
  return statuses;
}

// statusIds returns every known lifecycle status id, in display order.
export function statusIds() {
  return statuses.map((s) => s.id);
}

// attentionStatusIds is the set of statuses that count toward the "Needs you"
// filter (work that is waiting on a human).
export function attentionStatusIds() {
  return new Set(statuses.filter((s) => s.needs_you).map((s) => s.id));
}

// actionFor returns the presentation metadata for an operator control, or null
// if the backend has never seen it.
export function actionFor(id) {
  return actionById.get(id) || null;
}
