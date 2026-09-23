<script setup lang="ts">
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { workflows } from "./state";

/** Sharer of runs: how much of the work that finished passed its gates. */
const stats = computed(() => workflows.value?.workflows ?? []);

const totals = computed(() => ({
  runs: stats.value.reduce((a, w) => a + (w.runs || 0), 0),
  merged: stats.value.reduce((a, w) => a + (w.merged || 0) + (w.pr_open || 0), 0),
}));

const pct = computed(() => {
  if (!totals.value.runs) return 0;
  return Math.round((totals.value.merged / totals.value.runs) * 100);
});
</script>

<template>
  <Card v-if="workflows">
    <CardHeader>
      <CardTitle>Quality gates</CardTitle>
      <CardDescription>Work that passed its quality gates</CardDescription>
    </CardHeader>
    <CardContent>
      <Empty v-if="!totals.runs">
        <EmptyHeader>
          <EmptyTitle>No completed runs yet</EmptyTitle>
        </EmptyHeader>
      </Empty>
      <template v-else>
        <!--
          The rate is a number, not a gauge: one value carrying one meaning
          reads at a glance, and the rows under it are the actual signal.
          Per-workflow counts are the card's body; parked tasks are not
          listed here -- Throughput's "Needs you" tile already speaks for
          them, from the statuses themselves.
        -->
        <div class="flex items-baseline gap-2">
          <span class="text-3xl font-semibold">{{ pct }}%</span>
          <span class="text-xs text-fg-muted">pass rate · {{ totals.runs }} runs</span>
        </div>
        <ul class="mt-3 flex flex-col">
          <li
            v-for="(w, i) in stats.slice(0, 3)"
            :key="i"
            class="flex items-center justify-between gap-3 border-b border-hairline py-1.5 text-sm last:border-b-0"
          >
            <span class="truncate text-fg-muted">{{ w.workflow || "workflow" }}</span>
            <Badge :variant="(w.merged || 0) === (w.runs || 0) ? 'ok' : (w.parked || 0) > 0 ? 'warn' : 'info'">
              {{ w.merged || 0 }}/{{ w.runs || 0 }}
            </Badge>
          </li>
        </ul>
      </template>
    </CardContent>
  </Card>
</template>