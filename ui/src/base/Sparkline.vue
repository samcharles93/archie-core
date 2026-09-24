<script setup lang="ts">
import { computed } from "vue";

/**
 * Inline SVG sparkline. Shows shape, not exact values: the tile's headline
 * number carries the precision.
 */
const props = withDefaults(
  defineProps<{
    series: number[];
    stroke?: string;
    width?: number;
    height?: number;
  }>(),
  { stroke: "var(--ok)", width: 220, height: 34 },
);

const points = computed(() => {
  const max = Math.max(...props.series, 1);
  const min = Math.min(...props.series, 0);
  const span = max - min || 1;
  const step =
    props.series.length > 1
      ? props.width / (props.series.length - 1)
      : props.width;

  return props.series
    .map(
      (v, i) =>
        `${(i * step).toFixed(1)},${(props.height - ((v - min) / span) * props.height).toFixed(1)}`,
    )
    .join(" ");
});
</script>

<template>
  <svg
    class="mt-3 block h-[34px] w-full"
    :viewBox="`0 0 ${props.width} ${props.height}`"
    preserveAspectRatio="none"
    aria-hidden="true"
  >
    <polyline
      :points="points"
      fill="none"
      :stroke="props.stroke"
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
      vector-effect="non-scaling-stroke"
    />
  </svg>
</template>
