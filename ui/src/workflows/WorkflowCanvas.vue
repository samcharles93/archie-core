<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { VueFlow, type VueFlowStore } from "@vue-flow/core";
import { Background } from "@vue-flow/background";
import { Controls } from "@vue-flow/controls";
import "@vue-flow/core/dist/style.css";
import "@vue-flow/controls/dist/style.css";

import StepNode from "./StepNode.vue";
import { withRuns, workflowGraph, type StageRun } from "./workflow-graph";
import type { WorkflowTrigger } from "./workflow-triggers";

const props = defineProps<{
  yaml: string;
  stages?: StageRun[];
  selected?: (string | number)[] | null;
  triggers?: WorkflowTrigger[];
}>();
const emit = defineEmits<{ select: [(string | number)[] | null] }>();

const NODE_WIDTH = 240;

const container = ref<HTMLElement | null>(null);
const graph = computed(() => withRuns(workflowGraph(props.yaml), props.stages ?? []));
const selectedKey = computed(() => JSON.stringify(props.selected ?? null));
const nodes = computed(() =>
  graph.value.nodes.map((node) => {
    if (node.data.kind === "start") return { ...node, data: { ...node.data, triggers: props.triggers ?? [] } };
    // Each trigger past the second makes the start node a line taller.
    const drop = Math.max(0, (props.triggers?.length ?? 0) - 2) * 18;
    if (drop) node = { ...node, position: { ...node.position, y: node.position.y + drop } };
    return JSON.stringify(node.data.path ?? null) === selectedKey.value && props.selected
      ? { ...node, data: { ...node.data, selected: true } }
      : node;
  }),
);
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

const ZOOM = 0.9;
let flow: VueFlowStore | undefined;

// Start at a readable size rather than shrinking a long workflow until
// nothing on it can be read, scrolled to the furthest step the watched run has
// reached so the step that is moving stays in view.
function place(store?: VueFlowStore): void {
  const animate = !store;
  flow = store ?? flow;
  if (!flow) return;
  const width = container.value?.clientWidth ?? 800;
  const height = container.value?.clientHeight ?? 560;
  const reached = nodes.value.filter((node) => node.data.run).at(-1);
  const y = reached ? Math.min(24, height / 3 - reached.position.y * ZOOM) : 24;
  void flow.setViewport({ x: width / 2 - NODE_WIDTH / 2, y, zoom: ZOOM }, { duration: animate ? 300 : 0 });
}

watch(() => nodes.value.filter((node) => node.data.run).length, () => place());
</script>

<template>
  <div ref="container" class="workflow-canvas h-[560px] overflow-hidden rounded-lg border border-border bg-background">
    <VueFlow
      class="h-full"
      :nodes="nodes"
      :edges="edges"
      :nodes-draggable="false"
      :nodes-connectable="false"
      :elements-selectable="false"
      :min-zoom="0.3"
      :max-zoom="1.5"
      pan-on-scroll
      @pane-ready="place"
      @node-click="({ node }) => emit('select', node.data.path ?? null)"
      @pane-click="emit('select', null)"
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
@keyframes step-running {
  50% {
    box-shadow: 0 0 0 4px color-mix(in oklab, var(--color-info) 30%, transparent);
  }
}
.step-running {
  animation: step-running 1.6s ease-in-out infinite;
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
