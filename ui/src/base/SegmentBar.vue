<script setup lang="ts">
import { computed } from "vue";

import { STATUS_FILL, type StatusKind } from "@/lib/status";

/**
 * Segmented proportional bar with a legend.
 *
 * Used for budget composition -- what is spent, committed and still free --
 * because three related quantities are easier to compare in one bar than in
 * three separate numbers.
 */
const props = defineProps<{
  segments: Array<{ label: string; value: number; kind?: StatusKind }>;
}>();

const total = computed(
  () => props.segments.reduce((a, s) => a + (s.value || 0), 0) || 1,
);

const partWidth = (value: number) =>
  `${((value / total.value) * 100).toFixed(1)}%`;
const fill = (kind?: StatusKind) => STATUS_FILL[kind ?? "idle"];
</script>

<template>
  <div>
    <div class="flex h-[10px] gap-[2px] overflow-hidden rounded-full bg-border">
      <span
        v-for="s in props.segments.filter((s) => s.value > 0)"
        :key="s.label"
        class="block h-full"
        :class="fill(s.kind)"
        :style="`width:${partWidth(s.value)}`"
        :title="`${s.label}: ${s.value}`"
      />
    </div>
    <div class="mt-3 flex flex-wrap gap-4">
      <span
        v-for="s in props.segments"
        :key="s.label"
        class="inline-flex items-center gap-2 text-xs text-fg-muted"
      >
        <span class="size-2 rounded-full" :class="fill(s.kind)" />
        {{ s.label }}
      </span>
    </div>
  </div>
</template>
