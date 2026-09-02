import { el } from "./dom.jsx";

/**
 * Shared log-entry rendering. Both the daemon-wide log page
 * (ui/src/logs/logs.js) and a task's own attempt log
 * (ui/src/tasks/task-logs.js) render the same Entry shape from
 * internal/logging, so the row markup lives here rather than being
 * duplicated a second time -- see CLAUDE.md's "extract to base on the
 * second distinct consumer".
 */

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

export function logRow(entry) {
  const fields = entry.fields || {};
  return el(
    `div.log-row.log-${levelKind(entry.level)}`,
    el("span.log-time.mono", shortTime(entry.time)),
    el("span.log-level", (entry.level || "info").toUpperCase()),
    el(
      "span.log-body",
      el("span.log-msg", entry.message || entry.msg || ""),
      ...Object.entries(fields).map(([k, v]) =>
        el("span.log-field", el("span.log-field-key", k), String(fmtValue(v))),
      ),
    ),
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
