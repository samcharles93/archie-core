<script setup lang="ts">
import { ArrowLeft } from "@lucide/vue";
import { computed } from "vue";
import { RouterLink } from "vue-router";

import { Button } from "@/components/ui/button";

import StartNewRun from "./StartNewRun.vue";
import { useTaskRun } from "./use-task-run";

/**
 * The run page's head, one strip: the task, its workflow line, and the
 * actions. Back is page chrome; Start a new run lives here too,
 * since a restart is the page's one decision. The commit-discard consequence
 * is stated in the confirm dialog, not beside the button. The forge
 * coordinates (repo, issue, PR) sit at the row's far end: context, not
 * action, but read left-to-right as where this work lives.
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
    <div class="flex w-full flex-wrap items-center gap-2">
      <Button variant="outline" as-child>
        <RouterLink to="/tasks">
          <ArrowLeft data-icon="inline-start" />
          Back to tasks
        </RouterLink>
      </Button>
      <StartNewRun compact :id="id" class="mr-auto" />
      <a
        v-if="run.task?.repo_url"
        class="text-link hover:underline"
        :href="run.task.repo_url"
        target="_blank"
        rel="noreferrer"
      >
        {{ run.task.owner }}/{{ run.task.repo }}
      </a>
      <a
        v-if="run.task?.issue_url && run.task?.issue_number"
        class="text-link hover:underline"
        :href="run.task.issue_url"
        target="_blank"
        rel="noreferrer"
      >
        Issue #{{ run.task.issue_number }}
      </a>
      <a
        v-if="run.task?.pr_url && run.task?.pr_number"
        class="text-link hover:underline"
        :href="run.task.pr_url"
        target="_blank"
        rel="noreferrer"
      >
        PR #{{ run.task.pr_number }}
      </a>
    </div>
  </div>
</template>
