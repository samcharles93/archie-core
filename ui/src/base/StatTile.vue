<script setup lang="ts">
import { TrendingDown, TrendingUp } from "@lucide/vue";
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import Sparkline from "./Sparkline.vue";

/**
 * A KPI tile: label, big value, trend against a stated comparison window, and
 * a sparkline. The comparison line is not decoration -- a number without a
 * baseline cannot be acted on, so `compare` is required.
 *
 * trend > 0 renders as an increase. `goodDirection` says whether that is good,
 * because rising latency and rising success rate are not the same news.
 *
 * The headline value is rendered whatever its type, including 0 and "": a tile
 * that showed a label and a comparison line with no number above them read as
 * a broken dashboard on every fresh install.
 */
const props = withDefaults(
  defineProps<{
    label: string;
    value: string | number;
    unit?: string;
    trend?: number | null;
    compare: string;
    series?: number[];
    goodDirection?: "up" | "down";
  }>(),
  { series: () => [], goodDirection: "up" },
);

const dir = computed(() => (props.trend == null ? null : props.trend >= 0 ? "up" : "down"));
const good = computed(() => (dir.value == null ? null : dir.value === props.goodDirection));
const trendIcon = computed(() => (dir.value === "up" ? TrendingUp : TrendingDown));
const trendLabel = computed(() => `${Math.abs(props.trend ?? 0).toFixed(1)}%`);
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>{{ props.label }}</CardTitle>
      <CardDescription v-if="props.compare">{{ props.compare }}</CardDescription>
      <CardAction v-if="dir">
        <Badge :variant="good ? 'ok' : 'danger'">
          <component :is="trendIcon" />
          {{ trendLabel }}
        </Badge>
      </CardAction>
    </CardHeader>
    <CardContent>
      <div class="text-3xl font-semibold tracking-[-0.03em]">
        {{ props.value }}<span v-if="props.unit" class="ml-1 text-lg text-fg-muted">{{ props.unit }}</span>
      </div>
      <Sparkline
        v-if="props.series.length > 1"
        :series="props.series"
        :stroke="good === false ? 'var(--danger)' : 'var(--ok)'"
      />
    </CardContent>
  </Card>
</template>
