<script setup lang="ts">
import { computed } from "vue";

/**
 * Arc gauge for a single headline percentage.
 *
 * A percentage that decides whether you need to act deserves more than a line
 * of text: the arc is readable at a glance from across a room, which a number
 * is not. Colour follows the value, so "is this bad" is answered before the
 * digits are read.
 */
const props = withDefaults(defineProps<{ value: number | string; label: string; size?: number }>(), {
  size: 168,
});

const pct = computed(() => Math.max(0, Math.min(100, Number(props.value) || 0)));
const rounded = computed(() => Math.round(pct.value));
const stroke = computed(() => (pct.value >= 90 ? "var(--ok)" : pct.value >= 70 ? "var(--warn)" : "var(--danger)"));

// 240-degree arc, opening downward, so the value reads left-to-right.
const R = 62;
const SWEEP = 240;
const START = 150;
const circumference = 2 * Math.PI * R * (SWEEP / 360);

function arc(from: number, deg: number): string {
  const cx = props.size / 2;
  const cy = props.size / 2;
  const a0 = (from * Math.PI) / 180;
  const a1 = ((from + deg) * Math.PI) / 180;
  const large = deg > 180 ? 1 : 0;
  return [
    `M ${cx + R * Math.cos(a0)} ${cy + R * Math.sin(a0)}`,
    `A ${R} ${R} 0 ${large} 1 ${cx + R * Math.cos(a1)} ${cy + R * Math.sin(a1)}`,
  ].join(" ");
}
</script>

<template>
  <div class="relative grid place-items-center">
    <!--
      The label announces the rounded value. It used to announce the raw one, so
      a screen reader read "71.38461538461539%" before the same figure was
      shown rounded.
    -->
    <svg
      class="block w-full max-w-[200px]"
      :viewBox="`0 0 ${props.size} ${props.size * 0.78}`"
      role="img"
      :aria-label="`${props.label}: ${rounded}%`"
    >
      <path
        :d="arc(START, SWEEP)"
        fill="none"
        stroke="var(--border)"
        stroke-width="12"
        stroke-linecap="round"
      />
      <path
        :d="arc(START, SWEEP)"
        fill="none"
        :stroke="stroke"
        stroke-width="12"
        stroke-linecap="round"
        :stroke-dasharray="circumference"
        :stroke-dashoffset="circumference * (1 - pct / 100)"
        class="transition-[stroke-dashoffset] duration-[600ms] ease-[cubic-bezier(0.4,0,0.2,1)]"
      />
    </svg>
    <div class="absolute top-[52%] -translate-y-1/2 text-center">
      <div class="text-2xl font-semibold tracking-[-0.03em]">{{ rounded }}%</div>
      <div class="text-xs text-fg-muted">{{ props.label }}</div>
    </div>
  </div>
</template>
