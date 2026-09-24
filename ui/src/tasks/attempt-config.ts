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

const GO_DURATION_UNITS: Record<string, number> = {
  h: 3600_000,
  m: 60_000,
  s: 1000,
  ms: 1,
};

/**
 * The whole-task time limit the attempt ran under, in milliseconds, from its
 * captured config. The daemon writes it as a Go duration ("4h0m0s"); 0 means
 * no limit or none recorded.
 */
export function taskTimeLimitMs(event: unknown): number {
  const document = (event as { data?: { document?: unknown } } | null)?.data
    ?.document as { budgets?: { task_wall_clock?: unknown } } | undefined;
  const value = document?.budgets?.task_wall_clock;
  if (typeof value !== "string") return 0;
  const parts = [...value.matchAll(/(\d+(?:\.\d+)?)(ms|h|m|s)/g)];
  if (!parts.length || parts.map((p) => p[0]).join("") !== value) return 0;
  return parts.reduce(
    (total, [, n, unit]) => total + Number(n) * GO_DURATION_UNITS[unit],
    0,
  );
}
