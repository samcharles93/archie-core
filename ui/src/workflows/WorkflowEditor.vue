<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { RotateCcw, Trash2 } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import StepPanel from "./StepPanel.vue";
import WorkflowCanvas from "./WorkflowCanvas.vue";
import type { StepPath } from "./workflow-edit";
import { useWorkflowRuns } from "./workflow-runs";
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
const id = ref("");
const yaml = ref("");
const selectedStep = ref<StepPath | null>(null);
const localError = ref("");

const { runs, watched, stages, pick } = useWorkflowRuns(selected);

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
  const nextID = id.value.trim();
  if (!nextID) {
    localError.value = "Workflow ID is required.";
    return;
  }
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
  if (
    !selected.value ||
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
    <div class="space-y-1.5">
      <Label for="workflow-id">ID</Label>
      <Input id="workflow-id" v-model="id" name="workflow-id" required autocomplete="off" class="max-w-sm font-mono" />
    </div>
    <Tabs v-model="view" class="space-y-1.5">
      <div class="flex items-center gap-2">
        <TabsList>
          <TabsTrigger value="canvas">Canvas</TabsTrigger>
          <TabsTrigger value="yaml">YAML</TabsTrigger>
        </TabsList>
        <!-- Step settings are the server's to check, on save. -->
        <span
          class="ml-auto text-xs"
          :class="parsed.ok ? 'text-ok' : 'text-danger'"
          role="status"
          >{{ parsed.ok ? "✓" : "✕" }} {{ validationLabel(parsed) }}</span
        >
      </div>
      <TabsContent value="canvas" class="space-y-2">
        <label class="flex items-center gap-2 text-sm">
          <span class="text-muted-foreground">Watching</span>
          <select
            :value="watched"
            class="h-8 max-w-md min-w-60 rounded-md border border-input bg-background px-2 text-sm"
            @change="pick(($event.target as HTMLSelectElement).value)"
          >
            <option value="">No run, definition only</option>
            <option v-for="run in runs" :key="run.id" :value="String(run.id)">
              #{{ run.id }} {{ run.title || "untitled" }}{{ run.status ? ` · ${run.status}` : "" }}
            </option>
          </select>
          <span v-if="!runs.length" class="text-xs text-fg-subtle">This workflow has not run yet.</span>
        </label>
        <div class="grid gap-3 lg:grid-cols-[1fr_20rem]">
          <WorkflowCanvas :yaml="yaml" :stages="stages" :selected="selectedStep" @select="selectedStep = $event" />
          <StepPanel v-model:yaml="yaml" :path="selectedStep" :vocabulary="vocabulary" @select="selectedStep = $event" />
        </div>
      </TabsContent>
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
    <details v-if="vocabulary.length" class="text-sm">
      <summary class="cursor-pointer text-fg-subtle">Step types ({{ vocabulary.length }})</summary>
      <ul class="mt-2 flex flex-wrap gap-1.5" aria-label="Step types">
        <li
          v-for="info in vocabulary"
          :key="info.name"
          class="rounded border border-border bg-secondary px-2 py-0.5 font-mono text-xs"
          :title="info.needs_repository ? 'Needs a repository' : 'Runs without a repository'"
        >{{ info.name }}<span v-if="!info.needs_repository" class="text-fg-subtle"> · no repo</span></li>
      </ul>
    </details>
    <p v-if="localError || state.error" class="text-sm text-danger" role="alert">
      {{ localError || state.error }}
    </p>
    <div class="flex flex-wrap items-center gap-2 border-t border-border pt-4">
      <span v-if="state.resource" class="mr-auto font-mono text-xs text-fg-subtle">Version {{ state.resource.version }}</span>
      <Button v-if="selected" type="button" variant="ghost" class="text-danger" @click="remove"><Trash2 /> Remove</Button>
      <Button v-if="shippedEntry" type="button" variant="outline" @click="restoreOne"><RotateCcw /> Restore this workflow</Button>
      <!-- A definition the page can see is wrong is not submitted for the
           server to refuse: the indicator above the editor says what to fix. -->
      <Button type="submit" :disabled="state.saving || !parsed.ok"><Spinner v-if="state.saving" data-icon="inline-start" /> Validate &amp; save</Button>
    </div>
  </form>
</template>
