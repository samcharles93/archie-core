import { onMounted, ref } from "vue";

import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
import type { ActivityDetailInput } from "./activity-detail";
import type { Setup } from "./setup-preference";

/**
 * The dashboard's shared state.
 *
 * The health row, the throughput tiles, the token outlook and the activity
 * table all read it, and the hero's actions read the task counts, so it lives
 * in a module rather than being threaded down through a chain of props.
 */

export interface Summary {
  statuses?: Record<string, number>;
  tokens_by_day?: Array<{ tokens?: number }>;
}

export interface WorkflowStat {
  workflow?: string;
  runs?: number;
  merged?: number;
  pr_open?: number;
  parked?: number;
}

/** An event off the live stream: the payload the detail column reads, plus
 * enough to point the row at the task it belongs to. */
export interface ActivityEvent extends ActivityDetailInput {
  kind?: string;
  type?: string;
  task_id?: number | string;
  repo?: string;
  issue?: number | string;
  at?: string | number;
}

/** A task, as far as the dashboard's links read it. */
export interface DashboardTask {
  id: number | string;
  status?: string;
  owner?: string;
  repo?: string;
  issue_number?: number;
}

export const summary = ref<Summary | null>(null);
export const setup = ref<Setup | null>(null);
export const workflows = ref<{ workflows?: WorkflowStat[] } | null>(null);
// null means loading, [] means loaded.
export const tasks = ref<DashboardTask[] | null>(null);
export const error = ref<string | null>(null);

export const taskIDsBySource = ref(new Map<string, number>());

export async function loadDashboard(): Promise<void> {
  error.value = null;
  try {
    const [nextSummary, nextSetup, nextWorkflows, nextTasks] =
      await Promise.all([
        api.summary<Summary>(),
        api.setup<Setup>().catch(() => null),
        api.workflows<{ workflows?: WorkflowStat[] }>().catch(() => null),
        api.tasks<DashboardTask[]>().catch(() => []),
      ]);
    const map = new Map<string, number>();
    for (const task of nextTasks) {
      if (task.owner && task.repo && task.issue_number) {
        map.set(
          `${task.owner}/${task.repo}#${task.issue_number}`,
          Number(task.id),
        );
      }
    }
    taskIDsBySource.value = map;
    summary.value = nextSummary;
    setup.value = nextSetup;
    workflows.value = nextWorkflows;
    tasks.value = nextTasks;
  } catch (err) {
    error.value = String((err as Error).message || err);
  }
}

export function taskIDForEvent(
  event: ActivityEvent,
  bySource: Map<string, number>,
): number {
  if (Number(event.task_id) > 0) return Number(event.task_id);
  if (!event.repo || !event.issue) return 0;
  return Number(bySource.get(`${event.repo}#${event.issue}`)) || 0;
}

/** The task an activity row opens, or 0. An event carries the id, or a
 * repo/issue pair the task list resolves. */
export function taskIDFor(event: ActivityEvent): number {
  return taskIDForEvent(event, taskIDsBySource.value);
}

/** Owns the initial fetch and the event stream for the page's lifetime. */
export function useDashboard(): void {
  useLiveResource("tasks", () => void loadDashboard(), 500);
  onMounted(loadDashboard);
}
