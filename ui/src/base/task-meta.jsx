// Central lifecycle vocabulary for the dashboard. The backend is the source of
// truth: /api/task-meta returns the statuses (label, pill severity, "needs you"
// grouping), operator actions (label, button variant, confirm prompt), the
// changes_captured status labels and the config schema stamp. This module
// fetches that catalog and exposes the accessors the rest of the UI reads, so
// no surface keeps a hand-synced copy of the vocabulary.
//
// The defaults below are a freeze-dried snapshot that render immediately and
// keep working when archied is unreachable; loadTaskMeta() replaces them with
// the live catalog as soon as it arrives. They must match the backend catalog
// so drawing the page never flashes a stale label: the server half is pinned by
// internal/webui/testdata/task_meta.json and this snapshot by
// ui/test/task-meta-catalogue.test.js, which reads that same fixture.
import { api } from "./api.jsx";

const DEFAULT_STATUSES = [
  { id: "queued", label: "Queued", kind: "idle", needs_you: false },
  { id: "running", label: "Working", kind: "info", needs_you: false },
  { id: "waiting_human", label: "Waiting for you", kind: "warn", needs_you: true },
  { id: "pr_open", label: "In review", kind: "ok", needs_you: false },
  { id: "merged", label: "Merged", kind: "ok", needs_you: false },
  { id: "parked", label: "Parked", kind: "warn", needs_you: true },
  { id: "dead", label: "Stopped (too many retries)", kind: "danger", needs_you: false },
  { id: "rejected", label: "Rejected", kind: "danger", needs_you: false },
  { id: "closed_wont_do", label: "Won't do", kind: "idle", needs_you: false },
];

const DEFAULT_ACTIONS = [
  { id: "cancel", label: "Cancel", kind: "quiet", confirm: `Cancel "{title}"? This closes the forge issue.` },
  { id: "stop", label: "Stop", kind: "primary", confirm: `Stop "{title}"? Recoverable work will remain parked.` },
  { id: "approve", label: "Approve", kind: "primary", confirm: "" },
  { id: "reject", label: "Reject", kind: "quiet", confirm: `Reject "{title}"? This closes the forge issue.` },
  { id: "retry", label: "Retry", kind: "primary", confirm: "" },
  { id: "abandon", label: "Abandon", kind: "quiet", confirm: `Abandon "{title}"? This closes the forge issue.` },
  { id: "archive", label: "Archive", kind: "quiet", confirm: `Archive the local record for "{title}"?` },
  { id: "open_pr", label: "Open PR", kind: "link", confirm: "" },
  { id: "open_issue", label: "Open issue", kind: "link", confirm: "" },
];

// The change statuses mirror internal/domain/workflow/task's Change* constants
// and internal/events.ConfigCapturedSchema on the Go side; both are served by
// buildTaskMeta and pinned across the language boundary by the fixture test.
const DEFAULT_CHANGE_STATUSES = [
  { id: "added", label: "Added" },
  { id: "modified", label: "Modified" },
  { id: "deleted", label: "Deleted" },
  { id: "renamed", label: "Renamed" },
  { id: "typechange", label: "Type changed" },
];

const DEFAULT_CONFIG_SCHEMA = "archie/task-config@1";

let statuses = [...DEFAULT_STATUSES];
let actions = [...DEFAULT_ACTIONS];
let changeStatuses = [...DEFAULT_CHANGE_STATUSES];
let configSchemaValue = DEFAULT_CONFIG_SCHEMA;
let statusById = new Map(statuses.map((s) => [s.id, s]));
let actionById = new Map(actions.map((a) => [a.id, a]));
let changeStatusById = new Map(changeStatuses.map((s) => [s.id, s]));

function rebuildIndexes() {
  statusById = new Map(statuses.map((s) => [s.id, s]));
  actionById = new Map(actions.map((a) => [a.id, a]));
  changeStatusById = new Map(changeStatuses.map((s) => [s.id, s]));
}

// applyTaskMeta upgrades the dashboard from a server catalog, replacing only
// the keys the payload actually carries. A payload from a server that predates
// one of these keys is an upgrade path, not a downgrade: the snapshot for the
// missing key stays in place rather than being emptied.
export function applyTaskMeta(payload) {
  if (!payload || typeof payload !== "object") return;
  if (Array.isArray(payload.statuses)) statuses = payload.statuses;
  if (Array.isArray(payload.actions)) actions = payload.actions;
  if (Array.isArray(payload.change_statuses)) changeStatuses = payload.change_statuses;
  if (typeof payload.config_schema === "string" && payload.config_schema) {
    configSchemaValue = payload.config_schema;
  }
  rebuildIndexes();
}

// loadTaskMeta upgrades the dashboard from the server catalog. It never throws:
// a failed fetch keeps the snapshot so the UI still renders.
export async function loadTaskMeta() {
  try {
    applyTaskMeta(await api.taskMeta());
  } catch {
    // archied unreachable or not yet serving this route; keep the snapshot.
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

// actionCatalog returns every operator control in display order.
export function actionCatalog() {
  return actions;
}

// fileStatusLabel is the human-readable name for a changes_captured status.
// An unknown status is passed through, so a capture from a newer producer
// still names itself rather than rendering "unknown".
export function fileStatusLabel(id) {
  return changeStatusById.get(id)?.label || id || "unknown";
}

// changeStatusList returns every known change status in display order.
export function changeStatusList() {
  return changeStatuses;
}

// configSchema is the schema stamp a config_captured payload carries when this
// build understands it.
export function configSchema() {
  return configSchemaValue;
}
