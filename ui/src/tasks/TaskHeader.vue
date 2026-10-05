<script setup lang="ts">
import { computed } from "vue";
import { ArrowUpRight, CircleAlert } from "@lucide/vue";

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { StatusPill } from "@/components/ui/status-pill";
import { statusKind, statusLabel } from "@/lib/task-meta";
import DispatchLinks from "@/bindings/DispatchLinks.vue";
import StartNewRun from "./StartNewRun.vue";
import TaskRowActions from "./TaskRowActions.vue";
import { pillFor } from "./status-pill";
import { shownActionIds } from "./task-actions";
import { useTaskRun } from "./use-task-run";

/**
 * The task's head: where it sits, what it is, its state and forge links, and
 * the page's one decision, starting a new run. A parked task opens with the
 * reason it stopped.
 */
const props = defineProps<{ id: string }>();
const run = useTaskRun();

const title = computed(() => run.task?.title || `Task #${props.id}`);
const status = computed(() => run.task?.status ?? "");
const pill = computed(() => pillFor(statusKind(status.value)));
const parked = computed(() => status.value === "parked" && !!run.task?.park_reason);

// The head has room for one control: the reap an operator reaches for once a
// task is finished with. Whether this task offers it at all is the server's
// action list, never this page's opinion about the status.
const HEAD_ACTIONS = ["archive"];

// The run's inputs and written outputs, one row per name.
const io = computed(() => {
  const rows = (prefix: string, values?: Record<string, unknown>) =>
    Object.entries(values ?? {}).map(([name, value]) => ({
      label: `${prefix} ${name}`,
      value: typeof value === "string" ? value : JSON.stringify(value),
    }));
  return [...rows("input", run.attempts?.inputs), ...rows("output", run.attempts?.outputs)];
});
const headerActions = computed(() =>
  shownActionIds(run.task?.actions, HEAD_ACTIONS),
);
</script>

<template>
  <div class="mb-6">
    <Breadcrumb class="mb-2">
      <BreadcrumbList>
        <BreadcrumbItem><BreadcrumbLink as-child><RouterLink to="/tasks">Tasks</RouterLink></BreadcrumbLink></BreadcrumbItem>
        <BreadcrumbSeparator />
        <BreadcrumbItem><BreadcrumbPage class="font-mono">#{{ id }}</BreadcrumbPage></BreadcrumbItem>
      </BreadcrumbList>
    </Breadcrumb>
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div class="min-w-0">
        <h1 class="text-2xl font-semibold">{{ title }}</h1>
        <div class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
          <StatusPill v-if="status" :tone="pill.tone" :dot="pill.dot">{{ statusLabel(status) }}</StatusPill>
          <span v-if="run.task?.workflow" class="font-mono text-xs text-fg-muted">{{ run.task.workflow }}</span>
          <a
            v-if="run.task?.issue_url && run.task?.issue_number"
            class="inline-flex items-center gap-0.5 font-mono text-xs text-fg-muted hover:text-foreground"
            :href="run.task.issue_url"
            target="_blank"
            rel="noreferrer"
            >{{ run.task.owner }}/{{ run.task.repo }} #{{ run.task.issue_number }}<ArrowUpRight class="size-3" aria-hidden="true"
          /></a>
          <DispatchLinks kind="tasks" :id="id" />
          <a
            v-if="run.task?.pr_url && run.task?.pr_number"
            class="inline-flex items-center gap-0.5 font-mono text-xs text-fg-muted hover:text-foreground"
            :href="run.task.pr_url"
            target="_blank"
            rel="noreferrer"
            >PR #{{ run.task.pr_number }}<ArrowUpRight class="size-3" aria-hidden="true"
          /></a>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <TaskRowActions
          v-if="run.task && headerActions.length"
          :task="run.task"
          :only="HEAD_ACTIONS"
          @done="run.refreshAll()"
        />
        <StartNewRun compact :id="id" />
      </div>
    </div>

    <dl
      v-if="io.length || run.attempts?.called_by"
      class="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs"
    >
      <template v-if="run.attempts?.called_by">
        <dt class="text-fg-subtle">Called by</dt>
        <dd>
          <RouterLink :to="`/tasks/${run.attempts.called_by}`" class="font-mono text-fg-muted hover:text-foreground hover:underline"
            >task #{{ run.attempts.called_by }}</RouterLink
          >
        </dd>
      </template>
      <template v-for="row in io" :key="row.label">
        <dt class="text-fg-subtle">{{ row.label }}</dt>
        <dd class="font-mono break-words text-fg-muted">{{ row.value }}</dd>
      </template>
    </dl>

    <div
      v-if="parked"
      role="status"
      class="mt-4 flex items-start gap-3 rounded-md border border-warn/40 bg-warn-soft px-4 py-3"
    >
      <CircleAlert class="mt-0.5 size-4 shrink-0 text-warn" aria-hidden="true" />
      <div class="min-w-0">
        <p class="text-sm font-medium text-warn">Parked</p>
        <p class="mt-0.5 font-mono text-xs break-words text-fg-muted">{{ run.task?.park_reason }}</p>
      </div>
    </div>
  </div>
</template>
