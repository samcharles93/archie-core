/**
 * The task run page's model: what the page can show, the shapes it reads from
 * the daemon, and the URL contract that carries the open tab and the pinned
 * attempt.
 *
 * One task id, one page. The id rides in the path and the view state (`tab`,
 * `attempt`) rides in the query string, so every view of this page is a deep
 * link that survives a reload -- /tasks/42?tab=changes&attempt=2.
 *
 * The attempt is the unit of every panel. Nothing here merges two attempts:
 * the rail, the log, the capture and the config are all selected by attempt,
 * and the two views of the same task's attempts (the selector and the rail)
 * come from a single response.
 */
import type { LocationQuery, LocationQueryRaw } from "vue-router";

import type { LogEntry } from "@/lib/log";

export interface TaskRunTab {
  id: string;
  label: string;
}

/**
 * The inspector's tabs. The stage rail is not one of them: it is the page's
 * master pane, always visible, and selecting a stage commands the inspector
 * to the log tab with that stage's filter set (docs/prds/task-run-master-detail.md).
 */
export const RUN_TABS: TaskRunTab[] = [
  { id: "log", label: "Log" },
  { id: "changes", label: "Changed files" },
  { id: "config", label: "Configuration" },
  { id: "debug", label: "Debug" },
];

const TAB_IDS = new Set(RUN_TABS.map((tab) => tab.id));

/** URL ids from before the split-pane layout that must keep resolving. */
const LEGACY_TAB_IDS = new Set(["stages"]);

/** The task record as the task list serves it. */
export interface TaskRecord {
  id: number | string;
  title?: string;
  status?: string;
  workflow?: string;
  owner?: string;
  repo?: string;
  repo_url?: string;
  issue_number?: number;
  issue_url?: string;
  pr_number?: number;
  pr_url?: string;
  park_reason?: string;
  tokens_used?: number;
  actions?: string[];
}

/** One stage of one attempt, as `GET /api/tasks/{id}/attempts` returns it. */
export interface Stage {
  seq?: number;
  name?: string;
  status?: string;
  duration_ms?: number;
  error?: string;
}

export interface Attempt {
  attempt: number;
  status?: string;
  started_at?: string;
  duration_ms?: number;
  stages?: Stage[];
}

/**
 * The run history. `current_attempt` is 0 on the wire for a task with no run
 * at all, which is not an attempt number.
 */
export interface AttemptsState {
  attempts?: Attempt[];
  current_attempt?: number;
  unattributed_events?: number;
}

/** One event off the task's stream, as much of it as this page reads. */
export interface TaskEvent {
  kind?: string;
  type?: string;
  stage?: string;
  workflow?: string;
  attempt?: number;
  at?: string;
  detail?: string;
  data?: Record<string, unknown>;
}

export interface LogFilters {
  level: string;
  stage: string;
}

/** The attempt log read's three outcomes: disabled, not found, or entries. */
export interface LogState {
  found?: boolean;
  disabled?: boolean;
  attempt?: number;
  entries?: LogEntry[];
}

export interface CaptureFile {
  path?: string;
  old_path?: string;
  status?: string;
  binary?: boolean;
  additions?: number;
  deletions?: number;
}

export interface CaptureTotals {
  files?: number;
  additions?: number;
  deletions?: number;
}

/** One recorded change capture for an attempt. */
export interface Capture {
  captured_after?: string;
  captured_at?: string;
  stage?: string;
  owner?: string;
  repo?: string;
  repo_url?: string;
  branch?: string;
  base?: string;
  head_sha?: string;
  pr_number?: number;
  pr_url?: string;
  totals?: CaptureTotals;
  files?: CaptureFile[];
  truncated?: boolean;
}

export interface ChangesState {
  found?: boolean;
  captures?: Capture[];
}

/** The stored task record and its events, verbatim: nothing here projects it. */
export type DebugState = Record<string, unknown>;

export function parseTaskId(raw: unknown): number | null {
  const value = String(raw ?? "").trim();
  if (!/^\d+$/.test(value)) return null;
  const n = Number(value);
  return Number.isSafeInteger(n) && n > 0 ? n : null;
}

export function initialTab(query: LocationQuery | undefined): string {
  const requested = String(query?.tab ?? "");
  // tab=stages named the rail before the split pane; the rail is now always
  // visible, so the URL resolves to the inspector's first tab.
  if (LEGACY_TAB_IDS.has(requested)) return "log";
  return TAB_IDS.has(requested) ? requested : RUN_TABS[0].id;
}

/**
 * `?attempt=2` pins attempt 2. An absent, empty or non-numeric value follows
 * whatever the server reports as current, and 0 is the wire's own spelling of
 * "the current attempt" (taskLogTarget, internal/webui/api_tasks_logs.go), so
 * it selects the current attempt rather than a nonexistent attempt 0.
 */
export function initialAttempt(
  query: LocationQuery | undefined,
): number | null {
  const raw = query?.attempt;
  if (raw == null || raw === "") return null;
  const n = Number(raw);
  return Number.isInteger(n) && n > 0 ? n : null;
}

/** The query string a tab and an attempt are addressed by. */
export function runQuery(
  tab: string,
  attempt: number | null,
): LocationQueryRaw {
  const query: LocationQueryRaw = { tab: tab || RUN_TABS[0].id };
  if (attempt) query.attempt = String(attempt);
  return query;
}

export function attemptKey(id: number, attempt: number | null): string {
  return `${id}|${Number(attempt) || 0}`;
}

export const EMPTY_LOG_FILTERS: LogFilters = { level: "", stage: "" };

/**
 * The identity of a log read: a task, an attempt, and the filters that produced
 * the response. Keying by task alone cannot represent two attempts -- the bug
 * this replaces -- and keying without the filters would serve attempt 2's
 * unfiltered log in answer to a filtered request.
 */
export function logCacheKey(
  id: number,
  attempt: number | null,
  filters: LogFilters = EMPTY_LOG_FILTERS,
): string {
  const applied = filters || EMPTY_LOG_FILTERS;
  return [
    id,
    Number(attempt) || 0,
    applied.level || "",
    applied.stage || "",
  ].join("|");
}
