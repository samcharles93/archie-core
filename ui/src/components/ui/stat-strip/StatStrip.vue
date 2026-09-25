<script setup lang="ts">
import { cn } from "@/lib/utils";

defineProps<{
  cells: { label: string; value: string | number; sub?: string; tone?: "default" | "warn" | "danger" }[];
  class?: string;
}>();
</script>

<template>
  <dl
    :class="cn('grid auto-cols-fr grid-flow-col divide-x divide-border rounded-lg border border-border bg-card max-md:grid-flow-row max-md:divide-x-0 max-md:divide-y', $props.class)"
  >
    <div v-for="cell in cells" :key="cell.label" class="min-w-0 px-5 py-4">
      <dt class="truncate text-xs text-fg-subtle">{{ cell.label }}</dt>
      <dd
        :class="
          cn(
            'mt-1 truncate text-3xl font-semibold tabular-nums',
            cell.tone === 'warn' && 'text-warn',
            cell.tone === 'danger' && 'text-danger',
          )
        "
      >
        {{ cell.value }}
      </dd>
      <dd v-if="cell.sub" class="mt-1 truncate text-xs text-fg-subtle">{{ cell.sub }}</dd>
    </div>
  </dl>
</template>
