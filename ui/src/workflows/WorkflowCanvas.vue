<script setup lang="ts">
import { computed, ref } from "vue";
import { VueFlow, type VueFlowStore } from "@vue-flow/core";
import { Background } from "@vue-flow/background";
import { Controls } from "@vue-flow/controls";
import "@vue-flow/core/dist/style.css";
import "@vue-flow/controls/dist/style.css";

import StepNode from "./StepNode.vue";
import { workflowGraph } from "./workflow-graph";

const props = defineProps<{ yaml: string }>();

const NODE_WIDTH = 240;

const container = ref<HTMLElement | null>(null);
const graph = computed(() => workflowGraph(props.yaml));
const nodes = computed(() => graph.value.nodes);
const edges = computed(() =>
  graph.value.edges.map(({ label, offset, ...edge }) => ({
    ...edge,
    type: "smoothstep",
    pathOptions: { offset: offset ?? 20, borderRadius: 8 },
    class: edge.kind === "data" ? "workflow-data-edge" : "workflow-flow-edge",
    // A data edge names the result it carries on hover, not on the canvas.
    data: { label },
  })),
);

// Start at the top at a readable size; a long workflow scrolls down rather
// than shrinking until nothing on it can be read.
function place(flow: VueFlowStore): void {
  const width = container.value?.clientWidth ?? 800;
  void flow.setViewport({ x: width / 2 - NODE_WIDTH / 2, y: 24, zoom: 0.9 });
}
</script>

<template>
  <div ref="container" class="workflow-canvas h-[560px] overflow-hidden rounded-lg border border-border bg-background">
    <VueFlow
      :key="yaml"
      :nodes="nodes"
      :edges="edges"
      :nodes-draggable="false"
      :nodes-connectable="false"
      :elements-selectable="false"
      :min-zoom="0.3"
      :max-zoom="1.5"
      pan-on-scroll
      @pane-ready="place"
    >
      <template #node-step="nodeProps">
        <StepNode v-bind="nodeProps" />
      </template>
      <Background :gap="20" />
      <Controls :show-interactive="false" />
    </VueFlow>
  </div>
</template>

<style>
.workflow-flow-edge path {
  stroke: var(--color-muted-foreground);
  stroke-width: 1.5;
}
.workflow-data-edge path {
  stroke: var(--color-primary);
  stroke-dasharray: 5 4;
  stroke-width: 1.25;
}
.workflow-canvas .vue-flow__controls {
  box-shadow: none;
}
.workflow-canvas .vue-flow__controls-button {
  background: var(--color-card);
  border-color: var(--color-border);
  fill: var(--color-foreground);
}
.workflow-canvas .vue-flow__controls-button:hover {
  background: var(--color-secondary);
}
</style>
