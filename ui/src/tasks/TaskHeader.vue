<script setup lang="ts">
import { ArrowLeft, RefreshCw } from "@lucide/vue";
import { computed } from "vue";
import { RouterLink } from "vue-router";

import { Button } from "@/components/ui/button";

import { useTaskRun } from "./use-task-run";

/**
 * The run page's head: which task this is, and the two things an operator does
 * from here -- go back, or re-read everything.
 */
const props = defineProps<{ id: string }>();

const run = useTaskRun();

const title = computed(() => run.task?.title || `Task #${props.id}`);
const sub = computed(() => {
  const task = run.task;
  return [
    `One run of task ${props.id}`,
    task?.workflow ? `${task.workflow} workflow` : "",
    task?.repo ? `${task.owner}/${task.repo}` : "",
  ]
    .filter(Boolean)
    .join(" · ");
});
</script>

<template>
  <div class="mb-5 flex flex-wrap items-start justify-between gap-5">
    <div>
      <h1 class="text-3xl font-semibold tracking-[-0.03em]">{{ title }}</h1>
      <p class="mt-2 text-sm text-fg-muted">{{ sub }}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <Button variant="outline" as-child>
        <RouterLink to="/tasks">
          <ArrowLeft data-icon="inline-start" />
          Back to tasks
        </RouterLink>
      </Button>
      <Button @click="run.refreshAll">
        <RefreshCw data-icon="inline-start" />
        Refresh
      </Button>
    </div>
  </div>
</template>
