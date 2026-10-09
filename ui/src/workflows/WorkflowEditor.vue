<script setup lang="ts">
import ApplyStatusRows from "@/settings/ApplyStatusRows.vue";
import { computed, ref, watch } from "vue";
import { Ellipsis, RotateCcw, Trash2, X } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import StepRun from "./StepRun.vue";
import { restartAt, withRuns, workflowGraph } from "./workflow-graph";
import { api } from "@/lib/api";
import StepEditor from "./StepEditor.vue";
import WorkflowSettings from "./WorkflowSettings.vue";
import RunWorkflowButton from "./RunWorkflowButton.vue";
import TaskRowActions from "@/tasks/TaskRowActions.vue";
import WorkflowCanvas from "./WorkflowCanvas.vue";
import { deleteStep, duplicateStep, insertStep, moveStep, type StepPath } from "./workflow-edit";
import { useWorkflowRuns } from "./workflow-runs";
import { useWorkflowTriggers } from "./workflow-triggers";
import {
  cloneControlPlaneValue,
  removeWorkflowDefinition,
  upsertWorkflowDefinition,
  useControlPlaneStore,
  type WorkflowDefinitionCollection,
} from "@/stores/control-plane";
import {
  parseWorkflowYaml,
  validationLabel,
  yamlLines,
  yamlTokenClass,
} from "./workflow-yaml";

const store = useControlPlaneStore();
const state = computed(() => store.stateFor("workflow-definitions"));
const collection = computed(
  () =>
    (state.value.resource?.value as
      WorkflowDefinitionCollection | undefined) ?? { definitions: [] },
);
const shipped = computed<WorkflowDefinitionCollection>(() =>
  store.shippedWorkflows(),
);
// Which definition is open belongs to the page, which also lists them.
const selected = defineModel<string>("selected", { required: true });
defineProps<{ runsCount?: number }>();
const id = ref("");
const yaml = ref("");
const selectedStep = ref<StepPath | null>(null);
// The slide-in panel shows one thing: a step's editor, or the workflow's settings.
const settingsOpen = ref(false);
watch(selectedStep, (path) => {
  if (path) settingsOpen.value = false;
});
const localError = ref("");

const { runs, watched, watchedRun, stages, attempt, pick, follow, refresh } = useWorkflowRuns(selected);
const stepTab = ref("definition");
const selectedNode = computed(() => withRuns(workflowGraph(yaml.value), stages.value).nodes.find((node) => JSON.stringify(node.data.path) === JSON.stringify(selectedStep.value))?.data);
function openStep(path: StepPath): void {
 selectedStep.value = path;
 stepTab.value = selectedNode.value?.run && watchedRun.value ? "run" : "definition";
}
watch(watched, () => { selectedStep.value = null; restartError.value = ""; });

// A parked run restarts at a step through the retry action, keeping the
// results of the steps before it.
const restartable = computed(() => watchedRun.value?.status === "parked");
const selectedRestart = computed(() => restartable.value && selectedStep.value ? restartAt(withRuns(workflowGraph(yaml.value), stages.value), selectedStep.value) : null);
const restarting = ref(false);
const restartError = ref("");
async function restartFrom(from: string): Promise<void> {
  restartError.value = "";
  restarting.value = true;
  try {
    await api.taskAction(watched.value, "retry", { resume_from: from });
    await refresh();
  } catch (err) {
    restartError.value = err instanceof Error ? err.message : "Restart failed";
  } finally {
    restarting.value = false;
  }
}
const { triggers, reload: reloadTriggers } = useWorkflowTriggers(selected);

// Canvas edits are edits to the YAML; a newly placed step opens for editing.
const stepTypeNames = computed(() => vocabulary.value.map((info) => info.name));
function insertAt(after: number, type: string): void {
  stepTab.value = "definition";
  const inserted = insertStep(yaml.value, after, type);
  yaml.value = inserted.source;
  selectedStep.value = inserted.path;
}
function duplicateAt(path: StepPath): void {
  stepTab.value = "definition";
  const copied = duplicateStep(yaml.value, path);
  yaml.value = copied.source;
  selectedStep.value = copied.path;
}
function moveAt(path: StepPath, delta: -1 | 1): void {
  const moved = moveStep(yaml.value, path, delta);
  yaml.value = moved.source;
  if (selectedStep.value) selectedStep.value = moved.path;
}
function removeAt(path: StepPath): void {
  yaml.value = deleteStep(yaml.value, path);
  selectedStep.value = null;
}

const shippedEntry = computed(() =>
  shipped.value.definitions.find((entry) => entry.id === id.value),
);

function load(value: string): void {
  const entry = collection.value.definitions.find(
    (candidate) => candidate.id === value,
  );
  id.value = entry?.id ?? value;
  yaml.value = entry?.yaml ?? `id: ${value}\nsteps: []\n`;
  selectedStep.value = null;
  localError.value = "";
}

watch(selected, load, { immediate: true });
// The collection arrives after the first render; a later live update must not
// overwrite what is being typed, so it only fills an editor still empty.
watch(collection, () => {
  if (!yaml.value) load(selected.value);
});

async function save(next: WorkflowDefinitionCollection): Promise<boolean> {
  localError.value = "";
  return store.replace("workflow-definitions", next);
}

async function saveDraft(): Promise<void> {
  // The id is the one the YAML declares; renaming is editing it there.
  const nextID = parsed.value.ok ? parsed.value.id : "";
  if (!nextID) return;
  const previous = selected.value;
  let next = collection.value;
  if (previous && previous !== nextID)
    next = removeWorkflowDefinition(next, previous);
  if (
    await save(upsertWorkflowDefinition(next, { id: nextID, yaml: yaml.value }))
  )
    selected.value = nextID;
}

async function remove(): Promise<void> {
  if (!selected.value || !window.confirm(`Delete the ${selected.value} workflow?`)) return;
  if (
    !(await save(removeWorkflowDefinition(collection.value, selected.value)))
  )
    return;
  selected.value = "";
  id.value = "";
  yaml.value = "";
}

async function restoreOne(): Promise<void> {
  if (!shippedEntry.value) return;
  await save(
    upsertWorkflowDefinition(
      collection.value,
      cloneControlPlaneValue(shippedEntry.value),
    ),
  );
}

// What is typed, read against the served step vocabulary (workflow-yaml.ts).
// The Steps preview, the gutter and the validity indicator are three readings
// of one parse, so they cannot disagree about the same text. Step settings are
// not served, so a save the server refuses still reports its own reason below.
const vocabulary = computed(() => store.stepTypes());
const parsed = computed(() => parseWorkflowYaml(yaml.value, vocabulary.value));
const stored = computed(() => collection.value.definitions.find((entry) => entry.id === selected.value)?.yaml);
const dirty = computed(() => yaml.value !== (stored.value ?? ""));
const view = ref("canvas");
const lines = computed(() => yamlLines(yaml.value));

// The highlight layer sits behind the textarea and never scrolls by itself, so
// it follows the textarea's own scroll.
const scrolled = ref(0);
function syncScroll(event: Event): void {
  scrolled.value = (event.target as HTMLTextAreaElement).scrollTop;
}

</script>

<template>
  <form class="space-y-4" @submit.prevent="saveDraft">
    <ApplyStatusRows kind="workflow-definitions" />
    <Tabs v-model="view" class="space-y-1.5">
      <div class="flex items-center gap-2">
        <TabsList>
          <TabsTrigger value="canvas">Canvas</TabsTrigger>
          <TabsTrigger value="yaml">YAML</TabsTrigger>
          <TabsTrigger v-if="$slots.performance" value="performance">Performance</TabsTrigger>
          <TabsTrigger v-if="$slots.runs" value="runs">Runs <span class="ml-1 font-mono text-xs text-fg-subtle">{{ runsCount ?? 0 }}</span></TabsTrigger>
        </TabsList>
        <div v-if="runs.length && view === 'canvas'" class="flex h-8 min-w-0 items-center gap-1 rounded-md border border-input pr-1">
        <select
          :value="watched"
          aria-label="Run shown on the canvas"
          class="h-full min-w-0 max-w-64 truncate rounded-md bg-transparent px-2 text-[13px] outline-none"
          @change="pick(($event.target as HTMLSelectElement).value)"
        >
          <option value="">Definition only</option>
          <option v-for="run in runs" :key="run.id" :value="String(run.id)">
            Run #{{ run.id }} {{ run.title || "" }}{{ run.status ? ` · ${run.status}` : "" }}
          </option>
        </select>
        <template v-if="watchedRun">
          <RouterLink :to="`/tasks/${watchedRun.id}`" class="shrink-0 px-1 text-xs text-muted-foreground hover:text-foreground hover:underline">Open</RouterLink>
          <TaskRowActions :task="watchedRun" class="shrink-0" @done="refresh" />
        </template>
        </div>
        <template v-if="view === 'canvas' || view === 'yaml'">
        <span v-if="!parsed.ok" class="ml-auto max-w-md truncate text-xs text-danger" role="status" :title="validationLabel(parsed)">
          {{ validationLabel(parsed) }}
        </span>
        <span v-else-if="dirty" class="ml-auto text-xs text-fg-subtle">Unsaved changes</span>
        <span v-else class="ml-auto" />
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <Button type="button" variant="ghost" size="icon-sm" aria-label="More"><Ellipsis /></Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" class="w-48">
            <DropdownMenuItem v-if="dirty" @select="load(selected)"><RotateCcw /> Discard changes</DropdownMenuItem>
            <DropdownMenuItem v-if="shippedEntry" @select="restoreOne"><RotateCcw /> Restore shipped version</DropdownMenuItem>
            <DropdownMenuSeparator v-if="dirty || shippedEntry" />
            <DropdownMenuItem variant="destructive" @select="remove"><Trash2 /> Delete workflow</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <RunWorkflowButton v-if="stored !== undefined" :workflow="selected" :disabled="dirty" @started="(id) => { view = 'canvas'; void follow(id); }" />
        <Button type="submit" size="sm" :disabled="state.saving || !parsed.ok || !dirty">
          <Spinner v-if="state.saving" data-icon="inline-start" /> Save
        </Button>
        </template>
      </div>
      <TabsContent value="canvas" class="space-y-2">
        <!-- The canvas takes the height the viewport has left, so the page
             itself does not scroll; the canvas pans. -->
        <div class="relative h-[calc(100dvh-20.5rem)] min-h-[26rem]">
          <p v-if="restartError" role="alert" class="absolute bottom-3 left-14 z-10 max-w-xl rounded-md border border-danger/40 bg-card px-3 py-2 text-sm text-danger shadow-lg">{{ restartError }}</p>
          <WorkflowCanvas
            :yaml="yaml"
            :stages="stages"
            :selected="selectedStep"
            :triggers="triggers"
            :types="stepTypeNames"
            :restartable="restartable"
            @edit="openStep"
            @restart="restartFrom"
            @settings="selectedStep = null; settingsOpen = true"
            @insert="insertAt"
            @duplicate="duplicateAt"
            @move="moveAt"
            @remove="removeAt"
          />
          <Transition
            enter-from-class="translate-x-full opacity-0"
            leave-to-class="translate-x-full opacity-0"
            enter-active-class="transition duration-200"
            leave-active-class="transition duration-150"
          >
            <div v-if="settingsOpen" class="absolute inset-y-0 right-0 overflow-hidden rounded-r-lg">
              <WorkflowSettings v-model:yaml="yaml" :triggers="triggers" @close="settingsOpen = false" @bound="reloadTriggers" />
            </div>
          </Transition>
          <Transition
            enter-from-class="translate-x-full opacity-0"
            leave-to-class="translate-x-full opacity-0"
            enter-active-class="transition duration-200"
            leave-active-class="transition duration-150"
          >
            <div v-if="selectedStep" class="absolute inset-y-0 right-0 flex flex-col overflow-hidden rounded-r-lg border-l border-border bg-card shadow-xl">
              <template v-if="selectedNode?.run && watchedRun">
                <div class="flex items-center gap-2 border-b border-border px-3 py-2">
                  <Tabs v-model="stepTab"><TabsList><TabsTrigger value="run">Run</TabsTrigger><TabsTrigger value="definition">Definition</TabsTrigger></TabsList></Tabs>
                  <span class="min-w-0 flex-1 truncate text-xs" :title="selectedNode.run.name">{{ selectedNode.run.name }}</span>
                  <Button v-if="selectedRestart" type="button" size="sm" variant="outline" :disabled="restarting || selectedRestart.blocked" @click="restartFrom(selectedRestart.from)"><RotateCcw /> {{ selectedRestart.label }}</Button>
                  <Button type="button" variant="ghost" size="icon-sm" aria-label="Close step details" @click="selectedStep = null"><X /></Button>
                </div>
                <div v-if="stepTab === 'run'" class="min-h-0 w-[32rem] max-w-[calc(100vw-3rem)] flex-1 overflow-y-auto">
                  <StepRun :task="watchedRun" :attempt="attempt" :stage="selectedNode.run" :stages="stages" />
                </div>
              </template>
              <StepEditor
                :workflows="collection.definitions"
                class="min-h-0 flex-1"
                v-if="stepTab === 'definition' || !selectedNode?.run || !watchedRun"
                :key="JSON.stringify(selectedStep)"
                v-model:yaml="yaml"
                :path="selectedStep"
                :vocabulary="vocabulary"
                @close="selectedStep = null"
                @remove="removeAt(selectedStep)"
              />
            </div>
          </Transition>
        </div>
      </TabsContent>
      <TabsContent value="performance" class="grid gap-4"><slot name="performance" /></TabsContent>
      <TabsContent value="runs"><slot name="runs" /></TabsContent>
      <TabsContent value="yaml">
      <!--
        A gutter and a highlighted layer behind the live textarea, so the YAML
        is numbered and coloured where it is edited rather than in a second,
        read-only pane beside it. The layer is aria-hidden and never focusable:
        the textarea in front of it stays the only thing the operator and a
        screen reader reach.
      -->
      <div
        class="relative overflow-hidden rounded-lg border border-input font-mono text-[13px] leading-6 focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50"
      >
        <div
          aria-hidden="true"
          class="pointer-events-none absolute inset-y-0 left-0 w-11 select-none overflow-hidden border-r border-border bg-secondary/40 py-3 text-right"
        >
          <div :style="`transform:translateY(${-scrolled}px)`">
            <div
              v-for="line in lines"
              :key="line.number"
              class="min-h-6 pr-2 text-fg-subtle"
              >{{ line.number }}</div
            >
          </div>
        </div>
        <div
          aria-hidden="true"
          class="pointer-events-none absolute inset-0 overflow-hidden py-3 pr-3 pl-14 break-words whitespace-pre-wrap"
        >
          <div :style="`transform:translateY(${-scrolled}px)`">
            <div v-for="line in lines" :key="line.number" class="min-h-6">
              <span
                v-for="(token, i) in line.tokens"
                :key="i"
                :class="yamlTokenClass(token.kind)"
                >{{ token.text }}</span
              >
            </div>
          </div>
        </div>
        <Textarea
          id="workflow-yaml"
          aria-label="Workflow YAML"
          v-model="yaml"
          name="workflow-yaml"
          required
          class="relative min-h-96 resize-y rounded-none border-0 bg-transparent py-3 pr-3 pl-14 text-[13px] leading-6 text-transparent caret-foreground shadow-none md:text-[13px] focus-visible:ring-0"
          spellcheck="false"
          @scroll="syncScroll"
        />
      </div>
      </TabsContent>
    </Tabs>
    <p v-if="localError || state.error" class="text-sm text-danger" role="alert">
      {{ localError || state.error }}
    </p>
  </form>
</template>
