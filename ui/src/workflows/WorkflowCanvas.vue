<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { VueFlow, type NodeMouseEvent, type VueFlowStore } from "@vue-flow/core";
import { Background } from "@vue-flow/background";
import { Controls } from "@vue-flow/controls";
import { ArrowDown, ArrowUp, Copy, Pencil, Trash2 } from "@lucide/vue";
import "@vue-flow/core/dist/style.css";
import "@vue-flow/controls/dist/style.css";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import AddNode from "./AddNode.vue";
import InsertEdge from "./InsertEdge.vue";
import StepNode from "./StepNode.vue";
import StepTypeItems from "./StepTypeItems.vue";
import type { StepPath } from "./workflow-edit";
import { stepTitle, withRuns, workflowGraph, type StageRun, type StepNodeData } from "./workflow-graph";
import type { WorkflowTrigger } from "./workflow-triggers";

const props = defineProps<{
  yaml: string;
  stages?: StageRun[];
  selected?: StepPath | null;
  triggers?: WorkflowTrigger[];
  /** Step type names; when given, the canvas offers editing. */
  types?: string[];
}>();
const emit = defineEmits<{
  edit: [StepPath];
  insert: [after: number, type: string];
  duplicate: [StepPath];
  move: [StepPath, -1 | 1];
  remove: [StepPath];
}>();

const NODE_WIDTH = 240;
const editable = computed(() => !!props.types?.length);
const types = computed(() => props.types ?? []);

const container = ref<HTMLElement | null>(null);
const graph = computed(() => withRuns(workflowGraph(props.yaml), props.stages ?? []));
const selectedKey = computed(() => JSON.stringify(props.selected ?? null));
const stepCount = computed(() => graph.value.nodes.filter((node) => node.data.path?.length === 2).length);
const nodes = computed(() =>
  graph.value.nodes.map((node) => {
    if (node.type === "add")
      return { ...node, data: { types: types.value, editable: editable.value, onInsert: (type: string) => emit("insert", stepCount.value - 1, type) } };
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
  graph.value.edges.map(({ label, offset, insertAfter, ...edge }) =>
    edge.kind === "flow"
      ? {
          ...edge,
          type: "insert",
          data: { insertAfter, types: types.value, editable: editable.value, onInsert: (after: number, type: string) => emit("insert", after, type) },
        }
      : {
          ...edge,
          type: "smoothstep",
          pathOptions: { offset: offset ?? 20, borderRadius: 8 },
          class: "workflow-data-edge",
          data: { label },
        },
  ),
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
  const reached = graph.value.nodes.filter((node) => node.data.run).at(-1);
  const y = reached ? Math.min(24, height / 3 - reached.position.y * ZOOM) : 24;
  void flow.setViewport({ x: width / 2 - NODE_WIDTH / 2, y, zoom: ZOOM }, { duration: animate ? 300 : 0 });
}

watch(() => graph.value.nodes.filter((node) => node.data.run).length, () => place());

// The right-click menu opens at the pointer, for the step under it or for the
// canvas itself.
const menu = ref<{ open: boolean; x: number; y: number; step?: StepNodeData }>({ open: false, x: 0, y: 0 });
function openMenu(event: MouseEvent, step?: StepNodeData): void {
  if (!editable.value) return;
  event.preventDefault();
  const box = container.value?.getBoundingClientRect();
  menu.value = { open: true, x: event.clientX - (box?.left ?? 0), y: event.clientY - (box?.top ?? 0), step };
}
function onNodeMenu({ event, node }: NodeMouseEvent): void {
  if (node.type === "add") return;
  openMenu(event as MouseEvent, node.data as StepNodeData);
}
function onNodeClick({ node }: NodeMouseEvent): void {
  const path = (node.data as StepNodeData).path;
  if (editable.value && path) emit("edit", path);
}
const menuPath = computed(() => menu.value.step?.path);
const topIndex = computed(() => (menuPath.value?.length === 2 ? Number(menuPath.value[1]) : undefined));
</script>

<template>
  <div ref="container" class="workflow-canvas relative h-[620px] overflow-hidden rounded-lg border border-border bg-background">
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
      @node-click="onNodeClick"
      @node-context-menu="onNodeMenu"
      @pane-context-menu="openMenu($event as MouseEvent)"
    >
      <template #node-step="nodeProps">
        <StepNode v-bind="nodeProps" />
      </template>
      <template #node-add="nodeProps">
        <AddNode v-bind="nodeProps" />
      </template>
      <template #edge-insert="edgeProps">
        <InsertEdge v-bind="edgeProps" />
      </template>
      <Background :gap="20" />
      <Controls :show-interactive="false" />
    </VueFlow>

    <DropdownMenu v-model:open="menu.open">
      <DropdownMenuTrigger as-child>
        <span class="pointer-events-none absolute size-0" :style="{ left: `${menu.x}px`, top: `${menu.y}px` }" />
      </DropdownMenuTrigger>
      <DropdownMenuContent class="w-56" align="start">
        <template v-if="menu.step?.kind === 'step' && menuPath">
          <DropdownMenuLabel class="truncate">{{ menu.step.title || stepTitle(menu.step.type) }}</DropdownMenuLabel>
          <DropdownMenuItem @select="emit('edit', menuPath)"><Pencil /> Edit</DropdownMenuItem>
          <template v-if="topIndex !== undefined">
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>Insert before</DropdownMenuSubTrigger>
              <DropdownMenuSubContent class="w-52"><StepTypeItems :types="types" @pick="emit('insert', topIndex - 1, $event)" /></DropdownMenuSubContent>
            </DropdownMenuSub>
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>Insert after</DropdownMenuSubTrigger>
              <DropdownMenuSubContent class="w-52"><StepTypeItems :types="types" @pick="emit('insert', topIndex, $event)" /></DropdownMenuSubContent>
            </DropdownMenuSub>
          </template>
          <DropdownMenuItem @select="emit('duplicate', menuPath)"><Copy /> Duplicate</DropdownMenuItem>
          <DropdownMenuItem @select="emit('move', menuPath, -1)"><ArrowUp /> Move up</DropdownMenuItem>
          <DropdownMenuItem @select="emit('move', menuPath, 1)"><ArrowDown /> Move down</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" @select="emit('remove', menuPath)"><Trash2 /> Delete</DropdownMenuItem>
        </template>
        <template v-else>
          <DropdownMenuLabel>{{ menu.step?.kind === "start" ? "Add the first step" : "Add a step at the end" }}</DropdownMenuLabel>
          <StepTypeItems :types="types" @pick="emit('insert', menu.step?.kind === 'start' ? -1 : stepCount - 1, $event)" />
        </template>
      </DropdownMenuContent>
    </DropdownMenu>
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
