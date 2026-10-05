import { computed, ref, watch, type Ref } from "vue";

import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
import type { StageRun } from "./workflow-graph";

import type { Task } from "@/tasks/TaskRow.vue";

export type WorkflowRun = Task & { workflow?: string };

interface AttemptsView {
  attempts?: { attempt: number; stages?: StageRun[] }[];
}

const RECENT_RUNS = 20;

/**
 * The recent runs of one workflow and the step states of the one being
 * watched, kept current from the live task stream. The newest run is watched
 * until the operator picks another.
 */
export function useWorkflowRuns(workflow: Ref<string>) {
  const runs = ref<WorkflowRun[]>([]);
  const watched = ref("");
  const stages = ref<StageRun[]>([]);
  const attempt = ref(0);
  let picked = false;

  async function loadRuns(): Promise<void> {
    const tasks = (await api.tasks<WorkflowRun[]>().catch(() => [])) ?? [];
    runs.value = tasks
      .filter((task) => task.workflow === workflow.value)
      .sort((a, b) => Number(b.id) - Number(a.id))
      .slice(0, RECENT_RUNS);
    if (!picked || (watched.value && !runs.value.some((run) => String(run.id) === watched.value)))
      watched.value = runs.value[0] ? String(runs.value[0].id) : "";
  }

  async function loadStages(): Promise<void> {
    if (!watched.value) {
      stages.value = [];
      attempt.value = 0;
      return;
    }
    const requested = watched.value;
    const view = await api.taskAttempts<AttemptsView>(requested).catch(() => null);
    if (watched.value !== requested) return;
    const latest = [...(view?.attempts ?? [])].sort((a, b) => b.attempt - a.attempt)[0];
    stages.value = latest?.stages ?? [];
    attempt.value = latest?.attempt ?? 0;
  }

  // "Definition only" is a choice too, and a refresh must not undo it.
  function pick(id: string): void {
    picked = true;
    watched.value = id;
  }

  async function refresh(): Promise<void> {
    await loadRuns();
    await loadStages();
  }

  watch(workflow, () => {
    picked = false;
    void refresh();
  }, { immediate: true });
  watch(watched, () => {
    stages.value = [];
    attempt.value = 0;
    void loadStages();
  });
  useLiveResource("tasks", () => void refresh(), 300);

  const watchedRun = computed(() => runs.value.find((run) => String(run.id) === watched.value));

  /** Watches a run that just started, once the task list has it. */
  async function follow(id: number | string): Promise<void> {
    picked = true;
    watched.value = String(id);
    await refresh();
  }

  return { runs, watched, watchedRun, stages, attempt, pick, follow, refresh };
}
