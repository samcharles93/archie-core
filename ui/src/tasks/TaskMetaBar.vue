<script setup lang="ts">
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import type { StatusKind } from "@/lib/status";
import { statusKind, statusLabel } from "@/lib/task-meta";

import { useTaskRun } from "./use-task-run";

/**
 * The task's lifecycle status, the attempt this page is showing, and the forge
 * coordinates. All three come from reads the page already holds, so the bar
 * costs no request of its own.
 *
 * "Not read" and "loading" are kept apart from "no status": the task list is
 * a list of the 100 most recently updated, so a task outside it is a fact
 * about the read, not a fact about the task.
 */
const run = useTaskRun();

// The server owns the status vocabulary and answers with the same five kinds
// the Badge draws, which is what makes the cast honest rather than a guess.
const taskKind = computed<StatusKind>(() => statusKind(run.task?.status ?? "") as StatusKind);
const taskStatusLabel = computed(() => statusLabel(run.task?.status ?? ""));

const attemptText = computed(() => {
  if (run.attempts === null) return "run history unreadable";
  if (run.attempts === undefined) return "loading…";
  if (run.attemptNumber == null) return "none recorded";
  return `attempt ${run.attemptNumber} of ${run.currentAttempt ?? run.attemptNumber}`;
});
</script>

<template>
  <div class="-mt-2 mb-4 flex flex-wrap items-center gap-4 text-sm">
    <span class="inline-flex items-center gap-2">
      <span class="text-xs tracking-[0.05em] text-fg-muted uppercase">Task status</span>
      <span v-if="run.taskList === undefined" class="text-fg-muted">loading…</span>
      <Badge v-else-if="run.task" :variant="taskKind">{{ taskStatusLabel }}</Badge>
      <span v-else class="text-fg-muted">not read</span>
    </span>
    <span class="inline-flex items-center gap-2">
      <span class="text-xs tracking-[0.05em] text-fg-muted uppercase">Attempt</span>
      <span>{{ attemptText }}</span>
    </span>
    <a
      v-if="run.task?.repo_url"
      class="text-primary hover:underline"
      :href="run.task.repo_url"
      target="_blank"
      rel="noreferrer"
    >
      {{ run.task.owner }}/{{ run.task.repo }}
    </a>
    <a
      v-if="run.task?.issue_url && run.task?.issue_number"
      class="text-primary hover:underline"
      :href="run.task.issue_url"
      target="_blank"
      rel="noreferrer"
    >
      Issue #{{ run.task.issue_number }}
    </a>
    <a
      v-if="run.task?.pr_url && run.task?.pr_number"
      class="text-primary hover:underline"
      :href="run.task.pr_url"
      target="_blank"
      rel="noreferrer"
    >
      PR #{{ run.task.pr_number }}
    </a>
  </div>
</template>
