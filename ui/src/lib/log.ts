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

export function fmtValue(v: unknown): string {
  if (v == null) return "";
  return typeof v === "object" ? JSON.stringify(v) : String(v);
}

export function shortTime(value: string | number | Date): string {
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return "--:--:--";
  return d.toTimeString().slice(0, 8);
}
