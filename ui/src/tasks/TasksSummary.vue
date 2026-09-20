<script setup lang="ts">
import { computed } from "vue";

import StatTile from "@/base/StatTile.vue";
import { attentionStatusIds } from "@/lib/task-meta";
import type { Task } from "./TaskRow.vue";

/**
 * The four numbers that say what the board holds, counted from the tasks the
 * page already has rather than from an aggregate endpoint: the same list feeds
 * the table, so a tile and a row can never disagree.
 */
const props = defineProps<{ tasks: Task[] }>();

const counts = computed(() => {
  const tally: Record<string, number> = {};
  for (const task of props.tasks) {
    const status = task.status ?? "";
    tally[status] = (tally[status] ?? 0) + 1;
  }
  return tally;
});

const total = computed(() => props.tasks.length);
const working = computed(() => counts.value.running ?? 0);
const delivered = computed(() => (counts.value.merged ?? 0) + (counts.value.pr_open ?? 0));

// "Needs you" is the server's grouping, not a local pair of ids: the same set
// backs the ?status=needs_you filter, so the tile and the filter cannot drift.
const needsYou = computed(() => {
  const attention = attentionStatusIds();
  return Object.entries(counts.value).reduce((sum, [status, n]) => (attention.has(status) ? sum + n : sum), 0);
});
</script>

<template>
  <div class="mb-5 grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
    <StatTile
      label="Total tasks"
      :value="total"
      :compare="total ? 'Everything archied has ever picked up' : 'No tasks yet'"
    />
    <StatTile
      label="Working now"
      :value="working"
      :compare="total ? `${working} of ${total} in progress` : 'Nothing running'"
    />
    <StatTile
      label="Needs you"
      :value="needsYou"
      :compare="needsYou ? 'Parked or awaiting a reply' : 'Nothing is blocked'"
      good-direction="down"
    />
    <StatTile
      label="Delivered"
      :value="delivered"
      :compare="total ? `${Math.round((delivered / total) * 100)}% of all tasks` : 'No tasks yet'"
    />
  </div>
</template>
