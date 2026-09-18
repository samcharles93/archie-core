import { h } from "preact";

/**
 * Shared log-entry rendering. Both the daemon-wide log page
 * (ui/src/logs/logs.jsx) and a task's own attempt log
 * (ui/src/tasks/task-logs.jsx) render the same Entry shape from
 * internal/logging, so the row markup lives here rather than being
 * duplicated a second time -- see CLAUDE.md's "extract to base on the
 * second distinct consumer".
 */

// The level filter vocabulary. Server-side level filtering takes a CSV of
// levels (`splitCSV` in internal/webui/api_tasks_logs.go), so the combined
// options are a wire concern too, not a display choice. It lives here because
// both the daemon log page and a task attempt's log pane filter by level.
export const LOG_LEVELS = [
  { value: "", label: "All levels" },
  { value: "ERROR", label: "Errors" },
  { value: "WARN,ERROR", label: "Warnings and errors" },
  { value: "INFO,WARN,ERROR", label: "Info and above" },
  { value: "DEBUG", label: "Debug only" },
];

export function levelKind(level) {
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

export function LogRow({ entry }) {
  const fields = entry.fields || {};
  return (
    <div className={`log-row log-${levelKind(entry.level)}`}>
      <span className="log-time mono">{shortTime(entry.time)}</span>
      <span className="log-level">{(entry.level || "info").toUpperCase()}</span>
      <span className="log-body">
        <span className="log-msg">{entry.message || entry.msg || ""}</span>
        {Object.entries(fields).map(([k, v]) => (
          <span className="log-field" key={k}>
            <span className="log-field-key">{k}</span>
            {String(fmtValue(v))}
          </span>
        ))}
      </span>
    </div>
  );
}

export function fmtValue(v) {
  if (v == null) return "";
  return typeof v === "object" ? JSON.stringify(v) : v;
}

export function shortTime(value) {
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return "--:--:--";
  return d.toTimeString().slice(0, 8);
}
