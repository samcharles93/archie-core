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

export function selectConfigEvent(
  events: TaskEvent[] | null | undefined,
  attempt: number | null,
): TaskEvent | null {
  return (
    (events || []).find(
      (ev) =>
        ev &&
        ev.kind === "config_captured" &&
        Number(ev.attempt) === Number(attempt),
    ) || null
  );
}
