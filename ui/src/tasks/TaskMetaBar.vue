<script setup lang="ts">
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import type { StatusKind } from "@/lib/status";
import { statusKind, statusLabel } from "@/lib/task-meta";

import { useTaskRun } from "./use-task-run";

/**
 * The task's lifecycle status, plus whatever the page parks on the same row
 * (the attempt pager rides here, pushed right). Everything comes from reads
 * the page already holds, so the bar costs no request of its own.
 *
 * "Not read" and "loading" are kept apart from "no status": the task list is
 * a list of the 100 most recently updated, so a task outside it is a fact
 * about the read, not a fact about the task.
 */
const run = useTaskRun();

// The server owns the status vocabulary and answers with the same five kinds
// the Badge draws, which is what makes the cast honest rather than a guess.
const taskKind = computed<StatusKind>(
  () => statusKind(run.task?.status ?? "") as StatusKind,
);
const taskStatusLabel = computed(() => statusLabel(run.task?.status ?? ""));
</script>

<template>
  <div class="mb-4 flex flex-wrap items-center gap-3 text-sm">
    <span v-if="run.taskList === undefined" class="text-fg-muted"
      >loading…</span
    >
    <template v-else-if="run.task">
      <Badge :variant="taskKind">{{ taskStatusLabel }}</Badge>
    </template>
    <slot />
  </div>
</template>
