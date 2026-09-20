<script setup lang="ts">
import { computed } from "vue";

import StatTile from "@/base/StatTile.vue";
import { compact } from "@/lib/format";
import { summary } from "./state";

/** The throughput row: what is running, what needs a human, what shipped. */

const counts = computed(() => summary.value?.statuses ?? {});
const tokensByDay = computed(() => summary.value?.tokens_by_day ?? []);
const series = computed(() => tokensByDay.value.map((d) => d.tokens || 0));

const done = computed(() => (counts.value.merged || 0) + (counts.value.pr_open || 0));
const attention = computed(() => (counts.value.parked || 0) + (counts.value.waiting_human || 0));
const total = computed(() => Object.values(counts.value).reduce((a, b) => a + b, 0));
const used = computed(() => series.value.reduce((a, b) => a + b, 0));

/**
 * trendPct compares the newer half of a series with the older half. Below four
 * points there is no trend to read, only noise, so it returns null and the
 * tile shows no arrow.
 */
function trendPct(values: number[]): number | null {
  if (values.length < 4) return null;
  const mid = Math.floor(values.length / 2);
  const older = values.slice(0, mid).reduce((a, b) => a + b, 0);
  const newer = values.slice(mid).reduce((a, b) => a + b, 0);
  if (!older) return null;
  return ((newer - older) / older) * 100;
}
</script>

<template>
  <div class="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
    <StatTile
      label="Working now"
      :value="counts.running || 0"
      :compare="`${counts.queued || 0} waiting to start`"
      :series="series"
    />
    <StatTile
      label="Needs you"
      :value="attention"
      :compare="attention ? 'Parked or awaiting a reply' : 'Nothing is blocked'"
      good-direction="down"
    />
    <StatTile
      label="Delivered"
      :value="done"
      :compare="total ? `${Math.round((done / total) * 100)}% of all tasks` : 'No tasks yet'"
    />
    <StatTile
      label="Tokens used"
      :value="compact(used)"
      :compare="`Across ${tokensByDay.length || 0} days`"
      :trend="trendPct(series)"
      good-direction="down"
      :series="series"
    />
  </div>
</template>
