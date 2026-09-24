import type { ActivityEvent } from "./state";
import { activityDetail } from "./activity-detail.ts";

/**
 * The live activity feed's grouping projection.
 *
 * The daemon emits high-frequency background telemetry that arrives as dozens
 * of same-shape rows an hour — often as an interleave of kinds (a curator
 * cycle emits curator_run, session-memory and curator_action events that all
 * carry the same payload). Printed raw, the table stops being scannable and
 * real milestones drown.
 *
 * The rule: a *run of consecutive* events sharing one task and one rendered
 * detail collapses into a single group row. Kind is deliberately not part of
 * the key — an interleave of kinds over one payload is one cycle of noise,
 * not three events — so anything else stays on its own line, and a milestone
 * is never merged with an event carrying different information. The grouping
 * is consecutive, not global, so interleaved events keep their order and
 * nothing is reordered under the operator.
 *
 * This is a pure view projection over the store's raw stream: the store keeps
 * every event, and this module decides only how the feed presents them.
 */

/** One displayed row: either a single event (count 1) or a collapsed run. */
export interface ActivityGroup {
  /** Stable identity of the run: the grouping key of its first event. */
  key: string;
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

export function groupKey(detail: string, taskID: number | undefined): string {
  return `${detail}:${taskID ?? 0}`;
}

export function groupActivity(events: ActivityEvent[]): ActivityGroup[] {
  const groups: ActivityGroup[] = [];
  for (const event of events) {
    const label = event.kind || event.type || "event";
    const taskID = Number(event.task_id) || 0;
    const key = groupKey(activityDetail(event).text, taskID);
    const last = groups[groups.length - 1];
    if (last && last.key === key) {
      last.count += 1;
      last.events.push(event);
      continue;
    }
    groups.push({
      key,
      label,
      taskID,
      count: 1,
      representative: event,
      events: [event],
    });
  }
  return groups;
}
