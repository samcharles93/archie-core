/**
 * The per-attempt effective configuration.
 *
 * There is no config endpoint: the document arrives inside the task's event
 * stream, which the page already fetches for the stage rail's agent reports and
 * the timeline. So this panel issues no request of its own and adds nothing to
 * the API client contract.
 *
 * Selection is by kind AND attempt. Selecting by kind alone would show the
 * newest attempt's configuration on every attempt, which is exactly the
 * merge-attempts bug the attempt column exists to prevent.
 */
import type { TaskEvent } from "./task-run";

// The schema name is owned by the daemon (events.ConfigCapturedSchema in
// internal/events/events.go). A payload in an unrecognised schema is shown
// verbatim with a note rather than reinterpreted, so a server-side bump
// degrades to the raw view instead of rendering something wrong.
export const CONFIG_SCHEMA = "archie/task-config@1";

export function selectConfigEvent(
  events: TaskEvent[] | null | undefined,
  attempt: number | null,
): TaskEvent | null {
  return (
    (events || []).find((ev) => ev && ev.kind === "config_captured" && Number(ev.attempt) === Number(attempt)) ||
    null
  );
}
