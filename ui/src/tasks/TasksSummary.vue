<script setup lang="ts">
import { computed } from "vue";

import StatTile from "@/base/StatTile.vue";
import { delivered } from "@/lib/delivered";

/**
 * The four numbers that say what the board holds, counted by the server over
 * every task: the table pages, so counting its rows would count one page.
 */
const props = defineProps<{
  summary: { statuses?: Record<string, number>; needs_you?: number };
}>();

const counts = computed(() => props.summary.statuses ?? {});

const total = computed(() =>
  Object.values(counts.value).reduce((sum, n) => sum + n, 0),
);
const working = computed(() => counts.value.running ?? 0);
// Delivered counts everything whose work is out of archied's hands: merged,
// in review, or finished with no change to make (completed, which is how a
// no-change build and a no-code triage end).
const done = computed(() => delivered(counts.value) + (counts.value.pr_open ?? 0));

// "Needs you" is grouped by the server, the same set ?status=needs_you filters.
const needsYou = computed(() => props.summary.needs_you ?? 0);
</script>

<template>
  <div class="mb-5 grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
    <StatTile
      label="Total tasks"
      :value="total"
      :compare="
        total ? 'Everything archied has ever picked up' : 'No tasks yet'
      "
    />
    <StatTile
      label="Working now"
      :value="working"
      :compare="
        total ? `${working} of ${total} in progress` : 'Nothing running'
      "
    />
    <StatTile
      label="Needs you"
      :value="needsYou"
      :compare="needsYou ? 'Parked or awaiting a reply' : 'Nothing is blocked'"
      good-direction="down"
    />
    <StatTile
      label="Delivered"
      :value="done"
      :compare="
        total
          ? `${Math.round((done / total) * 100)}% of all tasks`
          : 'No tasks yet'
      "
    />
  </div>
</template>
