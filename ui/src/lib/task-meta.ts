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
//
// loadTaskMeta() is called once at boot by main.ts. The catalog is held in
// refs, so a late-arriving catalog re-renders whatever already read it; there
// is no separate "re-render the mounted route" step the way the Preact shell
// needed one.
//
// The dashboard draws a line here between two kinds of vocabulary. Anything
// that is *presentation* -- a status's label, pill severity and "needs you"
// grouping, or a control's label and confirm prompt -- is served, because the
// server owns the words as well as the ids.
import { computed, shallowRef } from "vue";

import { api } from "./api";

import {
  type ActionMeta,
  type ChangeStatusMeta,
  DEFAULT_ACTIONS,
  DEFAULT_CHANGE_STATUSES,
  DEFAULT_CONFIG_SCHEMA,
  DEFAULT_STATUSES,
  type StatusMeta,
} from "./task-meta-snapshot";

export type { ActionMeta, ChangeStatusMeta, StatusMeta };

const statuses = shallowRef<StatusMeta[]>([...DEFAULT_STATUSES]);
const actions = shallowRef<ActionMeta[]>([...DEFAULT_ACTIONS]);
const changeStatuses = shallowRef<ChangeStatusMeta[]>([...DEFAULT_CHANGE_STATUSES]);
const configSchemaRef = shallowRef<string>(DEFAULT_CONFIG_SCHEMA);

const statusById = computed(() => new Map(statuses.value.map((s) => [s.id, s])));
const actionById = computed(() => new Map(actions.value.map((a) => [a.id, a])));
const changeStatusById = computed(() => new Map(changeStatuses.value.map((c) => [c.id, c])));

// loadTaskMeta upgrades the dashboard from the server catalog. It never throws:
// a failed fetch keeps the defaults so the UI still renders.
export async function loadTaskMeta(): Promise<void> {
  try {
    const data = await api.taskMeta<{
      statuses?: StatusMeta[];
      actions?: ActionMeta[];
      change_statuses?: ChangeStatusMeta[];
      config_schema?: string;
    } | null>();
    if (Array.isArray(data?.statuses)) statuses.value = data.statuses;
    if (Array.isArray(data?.actions)) actions.value = data.actions;
    if (Array.isArray(data?.change_statuses)) changeStatuses.value = data.change_statuses;
    if (data?.config_schema) configSchemaRef.value = data.config_schema;
  } catch {
    // archied unreachable or not yet serving this route; keep the defaults.
  }
}

/** statusLabel is the human-readable name for a lifecycle status. */
export function statusLabel(id: string): string {
  return statusById.value.get(id)?.label || id || "Unknown";
}

/** statusKind is the pill severity token (idle, info, ok, warn, danger). */
export function statusKind(id: string): string {
  return statusById.value.get(id)?.kind || "idle";
}

/** statusList returns every known lifecycle status in display order. */
export function statusList(): StatusMeta[] {
  return statuses.value;
}

/** statusIds returns every known lifecycle status id, in display order. */
export function statusIds(): string[] {
  return statuses.value.map((s) => s.id);
}

/**
 * attentionStatusIds is the set of statuses that count toward the "Needs you"
 * filter (work that is waiting on a human).
 */
export function attentionStatusIds(): Set<string> {
  return new Set(statuses.value.filter((s) => s.needs_you).map((s) => s.id));
}

/**
 * actionFor returns the presentation metadata for an operator control, or null
 * if the backend has never seen it.
 */
export function actionFor(id: string): ActionMeta | null {
  return actionById.value.get(id) || null;
}

/**
 * changeStatusLabel names a changes_captured file status. An id the catalog
 * does not know renders as itself rather than breaking the row.
 */
export function changeStatusLabel(id: string | undefined): string {
  return (id && changeStatusById.value.get(id)?.label) || id || "unknown";
}

/** configSchema is the schema stamp a recognised config_captured payload carries. */
export function configSchema(): string {
  return configSchemaRef.value;
}
