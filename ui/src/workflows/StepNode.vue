<script setup lang="ts">
import { computed } from "vue";
import { Handle, Position } from "@vue-flow/core";
import { Flag, GitBranch, Play, Repeat, SkipForward, Webhook, CircleDot, BookOpen, Workflow } from "@lucide/vue";

import { stepIcon } from "./step-icons";
import type { StepNodeData } from "./workflow-graph";

const props = defineProps<{ data: StepNodeData }>();

const STATE_CLASSES: Record<string, string> = {
  ok: "border-ok/70",
  failed: "border-danger/80",
  interrupted: "border-warn/70",
  running: "border-info step-running",
  skipped: "border-dashed border-muted-foreground/60",
};

const stateClass = computed(() => {
  if (props.data.kind === "start") return "border-primary/60";
  return STATE_CLASSES[props.data.run?.status ?? ""] ?? "border-border";
});

const TRIGGER_ICONS = { issue: CircleDot, event: Webhook, playbook: BookOpen, workflow: Workflow };

const STATE_LABELS: Record<string, string> = {
  ok: "Done",
  failed: "Failed",
  interrupted: "Interrupted",
  running: "Running",
  skipped: "Skipped",
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
    :class="[stateClass, data.selected && 'ring-2 ring-ring', 'cursor-pointer transition-colors hover:border-muted-foreground']"
  >
    <Handle v-if="data.kind !== 'start'" type="target" :position="Position.Top" />
    <Handle id="data-in" type="target" :position="Position.Right" class="!opacity-0" />
    <div class="flex items-center gap-1.5">
      <Play v-if="data.kind === 'start'" class="size-3.5 text-primary" aria-hidden="true" />
      <component :is="stepIcon(data.type)" v-else class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span class="truncate text-sm font-medium" :title="data.title">{{ data.title }}</span>
      <span v-if="data.branch" class="ml-auto flex items-center gap-0.5 text-[11px] text-fg-subtle">
        <GitBranch class="size-3" aria-hidden="true" />{{ data.branch }}
      </span>
    </div>
    <div v-if="data.type && data.type !== data.title" class="truncate font-mono text-[11px] text-fg-subtle">{{ data.type }}</div>
    <ul v-if="data.kind === 'start' && data.triggers?.length" class="mt-1 space-y-0.5">
      <li v-for="trigger in data.triggers" :key="trigger.kind + trigger.label" class="flex items-center gap-1.5 text-xs" :title="trigger.detail">
        <component :is="TRIGGER_ICONS[trigger.kind]" class="size-3 shrink-0 text-primary" aria-hidden="true" />
        <span class="truncate">{{ trigger.label }}</span>
        <span v-if="trigger.detail" class="truncate text-fg-subtle">{{ trigger.detail }}</span>
      </li>
    </ul>
    <div v-else-if="data.kind === 'start'" class="mt-0.5 text-xs text-muted-foreground">Nothing starts it yet; run it by hand or bind an event to it.</div>
    <div v-else-if="data.detail" class="mt-0.5 truncate text-xs text-muted-foreground" :title="data.detail">{{ data.detail }}</div>
    <p v-if="data.summary" class="mt-1 line-clamp-2 text-xs leading-snug text-foreground/80" :title="data.summary">{{ data.summary }}</p>
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
      :class="{ 'text-ok': data.run.status === 'ok', 'text-danger': data.run.status === 'failed', 'text-info': data.run.status === 'running', 'text-warn': data.run.status === 'interrupted', 'text-fg-subtle': data.run.status === 'skipped' }"
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
