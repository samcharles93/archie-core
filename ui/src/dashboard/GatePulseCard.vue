<script setup lang="ts">
import { computed } from "vue";

import Gauge from "@/base/Gauge.vue";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { summary, workflows } from "./state";

/** Sharer of runs: how much of the work that finished passed its gates. */
const stats = computed(() => workflows.value?.workflows ?? []);

const totals = computed(() => ({
  runs: stats.value.reduce((a, w) => a + (w.runs || 0), 0),
  merged: stats.value.reduce((a, w) => a + (w.merged || 0) + (w.pr_open || 0), 0),
  parked: stats.value.reduce((a, w) => a + (w.parked || 0), 0),
}));
</script>

<template>
  <Card v-if="summary">
    <CardHeader>
      <CardTitle>Gate pulse</CardTitle>
      <CardDescription v-if="totals.runs">Work that passed its quality gates</CardDescription>
    </CardHeader>
    <CardContent>
      <Empty v-if="!totals.runs">
        <EmptyHeader>
          <EmptyTitle>No completed runs yet</EmptyTitle>
          <EmptyDescription>Once Archie finishes a task, its gate pass rate appears here.</EmptyDescription>
        </EmptyHeader>
      </Empty>
      <template v-else>
        <Gauge :value="(totals.merged / totals.runs) * 100" label="pass rate" />
        <ul class="mt-4 flex flex-col gap-2">
          <li
            v-for="(w, i) in stats.slice(0, 3)"
            :key="i"
            class="flex items-center justify-between gap-3 rounded-sm bg-muted px-3 py-2 text-sm"
          >
            <span class="truncate text-fg-muted">{{ w.workflow || "workflow" }}</span>
            <Badge :variant="(w.merged || 0) === (w.runs || 0) ? 'ok' : (w.parked || 0) > 0 ? 'warn' : 'info'">
              {{ w.merged || 0 }}/{{ w.runs || 0 }}
            </Badge>
          </li>
          <li
            v-if="totals.parked > 0"
            class="flex items-center justify-between gap-3 rounded-sm bg-muted px-3 py-2 text-sm"
          >
            <span class="truncate text-fg-muted">Parked, awaiting you</span>
            <Badge variant="warn">{{ totals.parked }}</Badge>
          </li>
        </ul>
      </template>
    </CardContent>
  </Card>
</template>
