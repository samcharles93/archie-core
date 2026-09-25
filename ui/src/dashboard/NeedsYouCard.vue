<script setup lang="ts">
import { computed } from "vue";

import { Button } from "@/components/ui/button";
import { StatusPill } from "@/components/ui/status-pill";
import { attentionStatusIds, statusLabel } from "@/lib/task-meta";
import TaskRowActions from "@/tasks/TaskRowActions.vue";
import type { Task } from "@/tasks/TaskRow.vue";
import { loadDashboard, tasks } from "./state";

/** Tasks waiting on a human, each with the reason and the next action. */
type AttentionTask = Task & { park_reason?: string };

const waiting = computed(() => {
  const ids = attentionStatusIds();
  return ((tasks.value ?? []) as AttentionTask[]).filter((t) => t.status && ids.has(t.status));
});

function meta(task: AttentionTask): string {
  const where = task.owner && task.repo ? `${task.owner}/${task.repo}${task.issue_number ? ` #${task.issue_number}` : ""}` : "";
  return [where, task.workflow, task.park_reason || task.stage].filter(Boolean).join(" · ");
}
</script>

<template>
  <section class="rounded-lg border border-border bg-card" aria-labelledby="needs-you">
    <header class="flex items-center gap-2 border-b border-border px-5 py-3.5">
      <h2 id="needs-you" class="text-[15px] font-medium">Needs you</h2>
      <StatusPill v-if="waiting.length" tone="warn">{{ waiting.length }}</StatusPill>
      <RouterLink to="/tasks?status=needs_you" class="ml-auto text-xs text-fg-muted hover:text-foreground">All tasks</RouterLink>
    </header>
    <p v-if="tasks === null" class="px-5 py-6 text-sm text-fg-subtle">Not loaded.</p>
    <p v-else-if="!waiting.length" class="px-5 py-6 text-sm text-fg-muted">Nothing is waiting on you.</p>
    <ul v-else class="divide-y divide-border">
      <li v-for="task in waiting" :key="task.id" class="flex flex-wrap items-center gap-3 px-5 py-3">
        <StatusPill tone="warn" class="shrink-0">{{ statusLabel(task.status ?? "") }}</StatusPill>
        <div class="min-w-0 flex-1">
          <RouterLink :to="`/tasks/${task.id}`" class="block truncate text-sm font-medium hover:underline">
            {{ task.title || `Task ${task.id}` }}
          </RouterLink>
          <p class="truncate font-mono text-xs text-fg-subtle">{{ meta(task) }}</p>
        </div>
        <Button variant="ghost" size="sm" as-child><RouterLink :to="`/tasks/${task.id}`">Open</RouterLink></Button>
        <TaskRowActions :task="task" @done="loadDashboard" />
      </li>
    </ul>
  </section>
</template>
