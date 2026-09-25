<script setup lang="ts">
import { computed } from "vue";
import { ArrowUpRight } from "@lucide/vue";

import { ago, compact } from "@/lib/format";
import { cn } from "@/lib/utils";
import { statusKind, statusLabel } from "@/lib/task-meta";
import StageRail from "./StageRail.vue";
import { pillFor } from "./status-pill";
import { useTaskRun } from "./use-task-run";
import { duration } from "./timeline-event";

/** The task's right rail: every attempt with its outcome, the selected
 * attempt's stages, and the task's details. */
const run = useTaskRun();

const attempts = computed(() => [...(run.attempts?.attempts ?? [])].reverse());
const dot = (status?: string) => {
  const d = pillFor(statusKind(status ?? "")).dot;
  return { live: "bg-primary", warn: "bg-warn", danger: "bg-danger", idle: "bg-idle" }[d];
};
const heading = "mb-2 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase";
</script>

<template>
  <aside class="grid gap-6" aria-label="Task details">
    <section v-if="attempts.length" aria-labelledby="rail-attempts">
      <h2 id="rail-attempts" :class="heading">Attempts</h2>
      <ul class="flex flex-col gap-0.5">
        <li v-for="a in attempts" :key="a.attempt">
          <button
            type="button"
            :aria-current="Number(a.attempt) === Number(run.attemptNumber) ? 'true' : undefined"
            :class="
              cn(
                'flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-left text-sm transition-colors hover:bg-secondary',
                Number(a.attempt) === Number(run.attemptNumber) && 'bg-secondary font-medium',
              )
            "
            @click="run.selectAttempt(Number(a.attempt))"
          >
            <span class="size-2 shrink-0 rounded-full" :class="dot(a.status)" aria-hidden="true" />
            <span class="flex-1">Attempt {{ a.attempt }}</span>
            <span class="text-xs text-fg-subtle">{{ a.status ? statusLabel(a.status) : "" }}</span>
          </button>
          <p v-if="a.started_at" class="px-3 pb-1 pl-7 text-xs text-fg-subtle">
            {{ ago(a.started_at) }}<template v-if="a.duration_ms"> · {{ duration(a.duration_ms) }}</template>
          </p>
        </li>
      </ul>
    </section>

    <section aria-labelledby="rail-stages">
      <h2 id="rail-stages" :class="heading">Stages</h2>
      <StageRail :state="run.attempts" />
    </section>

    <section v-if="run.task" aria-labelledby="rail-details">
      <h2 id="rail-details" :class="heading">Details</h2>
      <dl class="grid grid-cols-[6.5rem_1fr] gap-x-3 gap-y-2 text-sm">
        <dt class="text-fg-subtle">Workflow</dt>
        <dd class="truncate font-mono text-xs leading-5">{{ run.task.workflow || "none" }}</dd>
        <template v-if="run.task.repo">
          <dt class="text-fg-subtle">Repository</dt>
          <dd class="truncate font-mono text-xs leading-5">
            <a v-if="run.task.repo_url" :href="run.task.repo_url" target="_blank" rel="noreferrer" class="hover:underline"
              >{{ run.task.owner }}/{{ run.task.repo }}</a
            ><template v-else>{{ run.task.owner }}/{{ run.task.repo }}</template>
          </dd>
        </template>
        <template v-if="run.task.issue_url && run.task.issue_number">
          <dt class="text-fg-subtle">Issue</dt>
          <dd class="font-mono text-xs leading-5">
            <a :href="run.task.issue_url" target="_blank" rel="noreferrer" class="inline-flex items-center gap-0.5 hover:underline"
              >#{{ run.task.issue_number }}<ArrowUpRight class="size-3" aria-hidden="true"
            /></a>
          </dd>
        </template>
        <template v-if="run.task.tokens_used">
          <dt class="text-fg-subtle">Tokens</dt>
          <dd class="font-mono text-xs leading-5">{{ compact(run.task.tokens_used) }}</dd>
        </template>
      </dl>
    </section>
  </aside>
</template>
