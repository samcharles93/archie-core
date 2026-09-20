<script setup lang="ts">
import { ArrowLeft, RefreshCw } from "@lucide/vue";
import { computed } from "vue";
import { RouterLink } from "vue-router";

import { Button } from "@/components/ui/button";

import StartNewRun from "./StartNewRun.vue";
import { useTaskRun } from "./use-task-run";

/**
 * The run page's head, one strip: the task, its workflow line, and the
 * actions. Back and Refresh are page chrome; Start a new run lives here too,
 * since a restart is the page's one decision. The commit-discard consequence
 * is stated in the confirm dialog, not beside the button.
 */
const props = defineProps<{ id: string }>();

const run = useTaskRun();

const title = computed(() => run.task?.title || `Task #${props.id}`);
const workflow = computed(() => (run.task?.workflow ? `${run.task.workflow} workflow` : ""));
</script>

<template>
  <div class="mb-4 flex flex-wrap items-start justify-between gap-x-5 gap-y-2">
    <div class="min-w-0">
      <h1 class="text-2xl font-semibold tracking-[-0.02em]">{{ title }}</h1>
      <p v-if="workflow" class="mt-1 text-sm text-fg-muted">{{ workflow }}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <Button variant="outline" as-child>
        <RouterLink to="/tasks">
          <ArrowLeft data-icon="inline-start" />
          Back to tasks
        </RouterLink>
      </Button>
      <Button variant="outline" @click="run.refreshAll">
        <RefreshCw data-icon="inline-start" />
        Refresh
      </Button>
      <StartNewRun compact :id="id" />
    </div>
  </div>
</template>