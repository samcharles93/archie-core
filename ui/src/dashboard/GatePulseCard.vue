<script setup lang="ts">
import { computed } from "vue";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { delivered } from "@/lib/delivered";
import { STATUS_FILL } from "@/lib/status";
import { passBar } from "./pass-bar";
import { workflows, type WorkflowStat } from "./state";

/** Sharer of runs: how much of the work that finished passed its gates. */
const stats = computed(() => workflows.value?.workflows ?? []);

// A run that finished without opening a pull request (a no-change build, a
// triage that needed no code) is delivered like a merge or a PR in review, so
// both totals count it: the pass rate would otherwise read every `completed`
// row as a failed gate.
const totals = computed(() => ({
  runs: stats.value.reduce((a, w) => a + (w.runs || 0), 0),
  merged: stats.value.reduce(
    (a, w) => a + delivered(w) + (w.pr_open || 0),
    0,
  ),
}));

const pct = computed(() => {
  if (!totals.value.runs) return 0;
  return Math.round((totals.value.merged / totals.value.runs) * 100);
});

/** One row's bar: the share of that gate's runs which got through, and whether
 * the gate is failing -- both decided in pass-bar.ts. */
const rowBar = (stat: WorkflowStat) =>
  passBar(delivered(stat), stat.runs || 0);
</script>

<template>
  <Card v-if="workflows">
    <CardHeader>
      <CardTitle>Quality gates</CardTitle>
    </CardHeader>
    <CardContent>
      <Empty v-if="!totals.runs">
        <EmptyHeader>
          <EmptyTitle>No completed runs yet</EmptyTitle>
        </EmptyHeader>
      </Empty>
      <template v-else>
        <!--
          Two sections, read left to right: the rate on its own, and the
          per-workflow counts that explain it. Parked tasks are not listed
          here -- Throughput's Needs-you tile already speaks for them, from
          the statuses themselves.
        -->
        <div
          class="grid grid-cols-1 gap-6 md:grid-cols-[auto_minmax(0,1fr)]"
        >
          <div class="flex flex-col justify-center">
            <span class="text-3xl font-semibold">{{ pct }}%</span>
            <span class="text-xs text-fg-muted"
              >pass rate · {{ totals.runs }} runs</span
            >
          </div>
          <ul class="flex flex-col">
            <li
              v-for="(w, i) in stats.slice(0, 3)"
              :key="i"
              class="flex flex-col gap-1.5 border-b border-hairline py-2 last:border-b-0"
            >
              <span class="flex items-baseline justify-between gap-3 text-sm">
                <span class="truncate text-fg-muted">{{
                  w.workflow || "workflow"
                }}</span>
                <span class="shrink-0 font-mono text-xs text-fg-subtle">{{
                  delivered(w)
                }}/{{ w.runs || 0 }}</span>
              </span>
              <!-- The bar restates the counts as a length: a gate drifting down
                   is visible before the two numbers are read. Its track carries
                   the failure when there is no width to show. -->
              <span
                class="block h-1.5 overflow-hidden rounded-full"
                :class="STATUS_FILL[rowBar(w).track]"
              >
                <span
                  class="block h-full rounded-full"
                  :class="STATUS_FILL[rowBar(w).kind]"
                  :style="`width:${rowBar(w).pct}%`"
                />
              </span>
            </li>
          </ul>
        </div>
      </template>
    </CardContent>
  </Card>
</template>
