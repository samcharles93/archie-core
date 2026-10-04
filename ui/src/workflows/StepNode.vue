<script setup lang="ts">
import { Handle, Position } from "@vue-flow/core";
import { Flag, GitBranch, Play, Repeat, SkipForward } from "@lucide/vue";

import type { StepNodeData } from "./workflow-graph";

defineProps<{ data: StepNodeData }>();
</script>

<template>
  <div
    class="w-60 rounded-lg border bg-card px-3 py-2 text-left shadow-sm"
    :class="data.kind === 'start' ? 'border-primary/60' : 'border-border'"
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
    <Handle type="source" :position="Position.Bottom" />
    <Handle id="data-out" type="source" :position="Position.Right" class="!opacity-0" />
  </div>
</template>
