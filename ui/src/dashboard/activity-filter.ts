/**
 * The live activity feed's filter.
 *
 * Every event the dashboard receives belongs either to a task run or to the
 * daemon's own background work, and the two are read for different reasons:
 * "what is my work doing" against "what is the daemon doing". A task event is
 * one that resolves to a task -- the same test the table's rows already use to
 * link an event to a task -- so the filter can never disagree with the links
 * inside the rows it is filtering.
 */

export type ActivityFilter = "all" | "tasks" | "system";

/** An event as far as the filter reads it: the task it belongs to, if any. */
export interface ActivityFilterInput {
  task_id?: number | string | null;
}

/** The three views, in the order the header renders them. */
export const ACTIVITY_FILTERS: { value: ActivityFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "tasks", label: "Tasks" },
  { value: "system", label: "System" },
];

/** belongsToTask is the same test the rows use to point an event at a task. */
export function belongsToTask(
  event: ActivityFilterInput | null | undefined,
): boolean {
  return (Number(event?.task_id) || 0) > 0;
}

/** matchesActivityFilter decides one event, so a group row can be judged by the
 * events it stands for. */
export function matchesActivityFilter(
  event: ActivityFilterInput | null | undefined,
  filter: ActivityFilter,
): boolean {
  if (filter === "tasks") return belongsToTask(event);
  if (filter === "system") return !belongsToTask(event);
  return true;
}

/** filterActivity keeps the events a view is about, in arrival order. The two
 * narrow views partition the feed: nothing shows in both, nothing in neither. */
export function filterActivity<T extends ActivityFilterInput>(
  events: readonly T[],
  filter: ActivityFilter,
): T[] {
  return events.filter((event) => matchesActivityFilter(event, filter));
}

/**
 * activityEmptyTitle words the empty state. A feed with nothing in it and a
 * feed this view emptied are different facts, and a blank title means there are
 * rows to show, so the caller renders no empty state at all.
 */
export function activityEmptyTitle(
  visible: number,
  filter: ActivityFilter,
): string {
  if (visible > 0) return "";
  if (filter === "all") return "Waiting for activity";
  return filter === "tasks" ? "No task events yet" : "No system events yet";
}
