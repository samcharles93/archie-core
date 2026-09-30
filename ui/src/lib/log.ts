import type { StatusKind } from "./status";

/**
 * Log-entry vocabulary shared by the daemon-wide log page and a task's own
 * attempt log, which render the same entry shape from internal/logging.
 */

/**
 * The level filter vocabulary. Server-side level filtering takes a CSV of
 * levels (`splitCSV` in internal/webui/api_tasks_logs.go), so the combined
 * options are a wire concern too, not a display choice.
 */
export const LOG_LEVELS = [
  { value: "", label: "All levels" },
  { value: "ERROR", label: "Errors" },
  { value: "WARN,ERROR", label: "Warnings and errors" },
  { value: "INFO,WARN,ERROR", label: "Info and above" },
  { value: "DEBUG", label: "Debug only" },
];

/** The kinds a log level uses. Never `ok`: a log line is not a pass. */
export type LevelKind = Exclude<StatusKind, "ok">;

/** One entry as internal/logging serialises it. */
export interface LogEntry {
  time: string | number | Date;
  level?: string | null;
  message?: string;
  msg?: string;
  fields?: Record<string, unknown>;
}

/** The level label's colour, by kind. One place, so two log panes agree. */
export const LEVEL_COLOR: Record<LevelKind, string> = {
  danger: "text-danger",
  warn: "text-warn",
  info: "text-info",
  idle: "text-idle",
};

export function levelKind(level: string | null | undefined): LevelKind {
  switch ((level || "").toUpperCase()) {
    case "ERROR":
      return "danger";
    case "WARN":
      return "warn";
    case "DEBUG":
      return "idle";
    default:
      return "info";
  }
}

/**
 * The row's own treatment, by level. The level label carries the colour; the row
 * carries the weight: an ERROR line is a block the eye lands on, not red text
 * in a column of grey. Every row keeps a left edge of the same width, coloured
 * or not, so the three columns never shift between rows.
 */
export const LEVEL_ROW: Record<LevelKind, string> = {
  danger: "border-l-danger bg-danger-soft",
  warn: "border-l-transparent",
  info: "border-l-transparent",
  idle: "border-l-transparent",
};

/** The classes one row wears for its level. */
export function rowClass(level: string | null | undefined): string {
  return LEVEL_ROW[levelKind(level)];
}

/**
 * The end of an attempt's log: which attempt, and how much it wrote. The count
 * is the lines on screen, so a filtered pane says how much the filter left.
 */
export function attemptFooter(attempt: number, lines: number): string {
  const count = Math.max(0, Math.trunc(lines) || 0);
  return `End of attempt ${attempt} \u00b7 ${count} line${count === 1 ? "" : "s"}`;
}

export function fmtValue(v: unknown): string {
  if (v == null) return "";
  return typeof v === "object" ? JSON.stringify(v) : String(v);
}

export function shortTime(value: string | number | Date): string {
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return "--:--:--";
  return d.toTimeString().slice(0, 8);
}
