<script setup lang="ts">
import { computed } from "vue";

import SegmentBar from "@/base/SegmentBar.vue";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { compact } from "@/lib/format";
import { summary } from "./state";

/** What the current rate projects to over the next 30 days. */

const days = computed(() => summary.value?.tokens_by_day ?? []);
const used = computed(() =>
  days.value.reduce((a, d) => a + (d.tokens || 0), 0),
);
const recent = computed(() =>
  days.value.slice(-7).reduce((a, d) => a + (d.tokens || 0), 0),
);
const perDay = computed(() =>
  days.value.length ? used.value / days.value.length : 0,
);
const projected = computed(() => Math.round(perDay.value * 30));
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Token outlook</CardTitle>
    </CardHeader>
    <CardContent>
      <Empty v-if="!days.length">
        <EmptyHeader>
          <EmptyTitle>Nothing spent yet</EmptyTitle>
        </EmptyHeader>
      </Empty>
      <template v-else>
        <div class="mb-1 text-3xl font-semibold tracking-[-0.03em]">
          {{ compact(projected) }}
        </div>
        <div class="mb-4 text-xs text-fg-subtle">
          Based on {{ compact(Math.round(perDay)) }} per day over
          {{ days.length }} days
        </div>
        <SegmentBar
          :segments="[
            {
              label: `Last 7 days (${compact(recent)})`,
              value: recent,
              kind: 'info',
            },
            {
              label: `Earlier (${compact(used - recent)})`,
              value: Math.max(used - recent, 0),
              kind: 'idle',
            },
          ]"
        />
      </template>
    </CardContent>
  </Card>
</template>
