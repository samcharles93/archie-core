<script setup lang="ts">
import { computed } from "vue";

import { StatStrip } from "@/components/ui/stat-strip";
import { delivered } from "@/lib/delivered";
import { compact } from "@/lib/format";
import { attentionStatusIds } from "@/lib/task-meta";
import { summary } from "./state";

/** The throughput row: what is running, what needs a human, what shipped. */

const counts = computed(() => summary.value?.statuses ?? {});
const tokensByDay = computed(() => summary.value?.tokens_by_day ?? []);
const used = computed(() => tokensByDay.value.reduce((a, d) => a + (d.tokens || 0), 0));
// Delivered work is out of archied's hands: merged, finished with no change to
// make (`completed`), or open in review.
const done = computed(() => delivered(counts.value) + (counts.value.pr_open || 0));
// "Needs you" is the server's grouping (attentionStatusIds), the same one the
// Tasks page's needs_you filter reads.
const attention = computed(() => {
  const ids = attentionStatusIds();
  return Object.entries(counts.value).reduce((sum, [s, n]) => (ids.has(s) ? sum + (n || 0) : sum), 0);
});
const total = computed(() => Object.values(counts.value).reduce((a, b) => a + b, 0));

const cells = computed(() => [
  { label: "Working now", value: counts.value.running || 0, sub: `${counts.value.queued || 0} waiting to start` },
  {
    label: "Needs you",
    value: attention.value,
    sub: attention.value ? "Parked or awaiting a reply" : "Nothing is blocked",
    tone: attention.value ? ("warn" as const) : undefined,
  },
  {
    label: "Delivered",
    value: done.value,
    sub: total.value ? `${Math.round((done.value / total.value) * 100)}% of all tasks` : "No tasks yet",
  },
  { label: "Tokens used", value: compact(used.value), sub: `Across ${tokensByDay.value.length} days` },
]);
</script>

<template>
  <StatStrip :cells="cells" />
</template>
