<script setup lang="ts">
import { computed } from "vue";

import { STATUS_FILL, type StatusKind } from "@/lib/status";

/**
 * A small proportional bar under a rate. It carries the same shape in every
 * workflow table so two rates can be compared by eye without reading either.
 */
const props = withDefaults(defineProps<{ fraction: number; kind?: StatusKind }>(), { kind: "ok" });

const width = computed(() => `${Math.min(100, Math.max(0, props.fraction))}%`);
</script>

<template>
  <div class="mt-2 h-[5px] w-[140px] overflow-hidden rounded-full bg-border">
    <div class="h-full rounded-full transition-[width]" :class="STATUS_FILL[props.kind]" :style="`width:${width}`" />
  </div>
</template>
