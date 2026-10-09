<script setup lang="ts">
import { GitBranch, Split } from "@lucide/vue";

import type { StepNodeData } from "./workflow-graph";

defineProps<{ data: StepNodeData }>();
</script>

<template>
  <div class="pointer-events-none size-full rounded-xl border border-dashed border-border bg-muted/30">
    <div class="flex items-center gap-1 px-2.5 pt-1.5 text-[11px] text-fg-subtle">
      <template v-if="data.lane === 'switch'">
        <Split class="size-3" aria-hidden="true" />
        <span v-if="data.title === 'default'">otherwise</span>
        <span v-else class="font-mono">= {{ data.title }}</span>
      </template>
      <template v-else>
        <GitBranch class="size-3" aria-hidden="true" />{{ data.title }}
      </template>
    </div>
  </div>
</template>
