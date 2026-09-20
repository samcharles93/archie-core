<script setup lang="ts">
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import SlowestStagesCard from "./SlowestStagesCard.vue";
import StageFailuresCard from "./StageFailuresCard.vue";
import type { StageStats } from "./stages";

/**
 * The stage half of the page. With no stage data this is one card saying so,
 * rather than two empty tables filtered down to nothing.
 */
const props = defineProps<{ stages: StageStats[] }>();
</script>

<template>
  <div
    v-if="props.stages.length"
    class="mt-4 grid grid-cols-1 gap-4 min-[1080px]:grid-cols-[repeat(auto-fit,minmax(340px,1fr))]"
  >
    <SlowestStagesCard :stages="props.stages" />
    <StageFailuresCard :stages="props.stages" />
  </div>
  <Card v-else class="mt-4">
    <CardHeader>
      <CardTitle>Per stage</CardTitle>
    </CardHeader>
    <CardContent>
      <Empty>
        <EmptyHeader>
          <EmptyTitle>No stage data yet</EmptyTitle>
          <EmptyDescription>Stage timing and failures appear here once workflows have run.</EmptyDescription>
        </EmptyHeader>
      </Empty>
    </CardContent>
  </Card>
</template>
