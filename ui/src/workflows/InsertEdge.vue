<script setup lang="ts">
import { computed } from "vue";
import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps } from "@vue-flow/core";

import AddStepMenu from "./AddStepMenu.vue";
import type { StepPath } from "./workflow-edit";

const props = defineProps<EdgeProps<{ trace?: string; insertAfter?: StepPath; types: string[]; editable: boolean; onInsert: (after: StepPath, type: string) => void }>>();

const path = computed(() =>
  getSmoothStepPath({
    sourceX: props.sourceX,
    sourceY: props.sourceY,
    sourcePosition: props.sourcePosition,
    targetX: props.targetX,
    targetY: props.targetY,
    targetPosition: props.targetPosition,
    borderRadius: 8,
  }),
);
</script>

<template>
  <BaseEdge :id="id" :path="path[0]" :class="['workflow-flow-edge', data?.trace && `trace-${data.trace}`]" />
  <EdgeLabelRenderer v-if="data?.editable && data.insertAfter !== undefined">
    <div
      class="workflow-insert absolute"
      :style="{ transform: `translate(-50%, -50%) translate(${path[1]}px, ${path[2]}px)`, pointerEvents: 'all' }"
    >
      <AddStepMenu :types="data.types" :label="data.insertAfter.length > 2 ? `Add a step in ${data.insertAfter[3]}` : undefined" @pick="data.onInsert(data.insertAfter!, $event)" />
    </div>
  </EdgeLabelRenderer>
</template>
