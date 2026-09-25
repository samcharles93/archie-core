<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { RotateCcw, Trash2 } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import {
  cloneControlPlaneValue,
  removeWorkflowDefinition,
  upsertWorkflowDefinition,
  useControlPlaneStore,
  type WorkflowDefinitionCollection,
} from "@/stores/control-plane";

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
const localError = ref("");

const shippedEntry = computed(() =>
  shipped.value.definitions.find((entry) => entry.id === id.value),
);

function load(value: string): void {
  const entry = collection.value.definitions.find(
    (candidate) => candidate.id === value,
  );
  id.value = entry?.id ?? value;
  yaml.value = entry?.yaml ?? `id: ${value}\nsteps:\n  - type: `;
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

</script>

<template>
  <form class="space-y-4" @submit.prevent="saveDraft">
    <div class="space-y-1.5">
      <Label for="workflow-id">ID</Label>
      <Input id="workflow-id" v-model="id" name="workflow-id" required autocomplete="off" class="max-w-sm font-mono" />
    </div>
    <div class="space-y-1.5">
      <Label for="workflow-yaml">YAML</Label>
      <Textarea
        id="workflow-yaml"
        v-model="yaml"
        name="workflow-yaml"
        required
        class="min-h-96 resize-y font-mono text-[13px]"
        spellcheck="false"
      />
    </div>
    <p v-if="localError || state.error" class="text-sm text-danger" role="alert">
      {{ localError || state.error }}
    </p>
    <div class="flex flex-wrap items-center gap-2 border-t border-border pt-4">
      <span v-if="state.resource" class="mr-auto font-mono text-xs text-fg-subtle">Version {{ state.resource.version }}</span>
      <Button v-if="selected" type="button" variant="ghost" class="text-danger" @click="remove"><Trash2 /> Remove</Button>
      <Button v-if="shippedEntry" type="button" variant="outline" @click="restoreOne"><RotateCcw /> Restore this workflow</Button>
      <Button type="submit" :disabled="state.saving"><Spinner v-if="state.saving" data-icon="inline-start" /> Validate &amp; save</Button>
    </div>
  </form>
</template>
