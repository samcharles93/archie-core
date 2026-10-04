<script setup lang="ts">
import { computed } from "vue";
import { Handle, Position } from "@vue-flow/core";
import { Flag, GitBranch, Play, Repeat, SkipForward } from "@lucide/vue";

import type { StepNodeData } from "./workflow-graph";

const props = defineProps<{ data: StepNodeData }>();

const STATE_CLASSES: Record<string, string> = {
  ok: "border-ok/70",
  failed: "border-danger/80",
  interrupted: "border-warn/70",
  running: "border-info step-running",
};

const stateClass = computed(() => {
  if (props.data.kind === "start") return "border-primary/60";
  return STATE_CLASSES[props.data.run?.status ?? ""] ?? "border-border";
});

const STATE_LABELS: Record<string, string> = {
  ok: "Done",
  failed: "Failed",
  interrupted: "Interrupted",
  running: "Running",
};

function duration(ms?: number): string {
  if (ms === undefined) return "";
  if (ms < 1000) return `${ms} ms`;
  const seconds = Math.round(ms / 1000);
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
}
</script>

<template>
  <div
    class="w-60 rounded-lg border bg-card px-3 py-2 text-left shadow-sm"
    :class="[stateClass, data.run ? '' : data.kind === 'step' && 'opacity-90']"
  >
    <Handle v-if="data.kind !== 'start'" type="target" :position="Position.Top" />
    <Handle id="data-in" type="target" :position="Position.Right" class="!opacity-0" />
    <div class="flex items-center gap-1.5">
      <Play v-if="data.kind === 'start'" class="size-3.5 text-primary" aria-hidden="true" />
      <span class="truncate text-sm font-medium" :title="data.title">{{ data.title }}</span>
      <span v-if="data.branch" class="ml-auto flex items-center gap-0.5 text-[11px] text-fg-subtle">
        <GitBranch class="size-3" aria-hidden="true" />{{ data.branch }}
      </span>
    </div>
    <div v-if="data.type && data.type !== data.title" class="truncate font-mono text-[11px] text-fg-subtle">{{ data.type }}</div>
    <div v-if="data.detail" class="mt-0.5 truncate text-xs text-muted-foreground" :title="data.detail">{{ data.detail }}</div>
    <div v-if="data.when || data.retry || data.continues" class="mt-1 flex flex-wrap gap-1 text-[11px]">
      <span v-if="data.when" class="flex max-w-full min-w-0 items-center gap-0.5 rounded bg-secondary px-1 font-mono" :title="`Runs only when ${data.when}`">
        <Flag class="size-3 shrink-0" aria-hidden="true" /><span class="truncate">{{ data.when }}</span>
      </span>
      <span v-if="data.retry" class="flex items-center gap-0.5 rounded bg-secondary px-1" :title="`Up to ${data.retry} attempts`">
        <Repeat class="size-3" aria-hidden="true" />×{{ data.retry }}
      </span>
      <span v-if="data.continues" class="flex items-center gap-0.5 rounded bg-secondary px-1" title="A failure does not stop the run">
        <SkipForward class="size-3" aria-hidden="true" />continues
      </span>
    </div>
    <div
      v-if="data.run"
      class="mt-1 flex items-center gap-1.5 text-[11px]"
      :class="{ 'text-ok': data.run.status === 'ok', 'text-danger': data.run.status === 'failed', 'text-info': data.run.status === 'running', 'text-warn': data.run.status === 'interrupted' }"
      :title="data.run.error || undefined"
    >
      <span class="size-1.5 rounded-full bg-current" aria-hidden="true" />
      {{ STATE_LABELS[data.run.status] ?? data.run.status }}
      <span v-if="data.run.duration_ms !== undefined" class="text-fg-subtle">{{ duration(data.run.duration_ms) }}</span>
      <span v-if="data.run.error" class="truncate text-danger">{{ data.run.error }}</span>
    </div>
    <Handle type="source" :position="Position.Bottom" />
    <Handle id="data-out" type="source" :position="Position.Right" class="!opacity-0" />
  </div>
</template>
