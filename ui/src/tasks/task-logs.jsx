import { el, empty } from "../base/dom.jsx";
import { logRow } from "../base/log-row.jsx";

/**
 * Renders one task's persisted attempt log (GET /api/tasks/:id/logs) --
 * stage timings, iteration counts, model, token/prompt/completion/cached
 * breakdowns -- the detail the daemon-wide log page never carries because it
 * only sees the daemon's own component-scoped lines.
 *
 * `state` is one of the values loadTaskLogs (in tasks.js) puts in its cache:
 * undefined while loading, null on fetch failure, or the API response body
 * once it lands. Row rendering itself is shared with the daemon-wide log via
 * ui/src/base/log-row.js -- this module only owns the task-attempt framing
 * and the "no persisted log for this attempt" / "log unavailable" states.
 */
export function taskLogPanel(state, opts = {}) {
  if (state === undefined) {
    return el("div.task-log-loading", "Loading attempt log…");
  }
  if (state === null) {
    return el(
      "div.task-log-error",
      el("div.task-timeline-detail", "Could not load this attempt's log."),
      el("button.btn", { onclick: opts.onRetry }, "Retry"),
    );
  }
  if (state.disabled) {
    return empty("No persisted log for this attempt", "Task logging is optional and was not enabled for this run.");
  }
  const entries = state.entries || [];
  if (!entries.length) {
    return empty("Nothing recorded", "This attempt's log file has no entries matching the current filter.");
  }
  return el("div.log-list.task-log-list", ...entries.map(logRow));
}
