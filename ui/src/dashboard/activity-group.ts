import type { ActivityEvent } from "./state";

/**
 * The live activity feed's grouping projection.
 *
 * The daemon emits high-frequency background telemetry (session-memory
 * cycles, runner calls) that arrives as dozens of same-shape rows an hour.
 * Printed raw, the table stops being scannable and real milestones drown.
 * The rule: a *run of consecutive* events sharing one label and one task
 * collapses into a single group row; anything else stays on its own line, so
 * a milestone is never merged with a different event that merely shares its
 * kind. The grouping is consecutive, not global, so interleaved kinds keep
 * their order and nothing is reordered under the operator.
 *
 * This is a pure view projection over the store's raw stream: the store keeps
 * every event, and this module decides only how the feed presents them.
 */

/** One displayed row: either a single event (count 1) or a collapsed run. */
export interface ActivityGroup {
  /** The resolved label the row shows, matching the Event column's fallback. */
  label: string;
  /** The task the group's events resolve to, or 0 for none. */
  taskID: number;
  /** How many events the group carries; 1 means an uncollapsed row. */
  count: number;
  /** The newest event of the run -- what the row shows while collapsed. */
  representative: ActivityEvent;
  /** Every event in the run, newest first. */
  events: ActivityEvent[];
}

export function groupKey(label: string, taskID: number | undefined): string {
  return `${label}:${taskID ?? 0}`;
}

export function groupActivity(events: ActivityEvent[]): ActivityGroup[] {
  const groups: ActivityGroup[] = [];
  for (const event of events) {
    const label = event.kind || event.type || "event";
    const taskID = Number(event.task_id) || 0;
    const last = groups[groups.length - 1];
    if (last && last.label === label && last.taskID === taskID) {
      last.count += 1;
      last.events.push(event);
      continue;
    }
    groups.push({ label, taskID, count: 1, representative: event, events: [event] });
  }
  return groups;
}