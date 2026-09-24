<script setup lang="ts">
import { computed, ref, watchEffect } from "vue";
import { Plus, RotateCcw, Trash2 } from "@lucide/vue";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import {
  cloneControlPlaneValue,
  removeWorkflowDefinition,
  upsertWorkflowDefinition,
  useControlPlaneStore,
  type WorkflowDefinitionCollection,
} from "@/stores/control-plane";
import ConfigCard from "@/settings/ConfigCard.vue";

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
const selected = ref("");
const id = ref("");
const yaml = ref("");
const localError = ref("");

const shippedEntry = computed(() =>
  shipped.value.definitions.find((entry) => entry.id === id.value),
);

watchEffect(() => {
  const entries = collection.value.definitions;
  if (
    entries.length &&
    (!selected.value || !entries.some((entry) => entry.id === selected.value))
  ) {
    select(entries[0].id);
  }
});

function select(value: string): void {
  selected.value = value;
  const entry = collection.value.definitions.find(
    (candidate) => candidate.id === value,
  );
  id.value = entry?.id ?? value;
  yaml.value = entry?.yaml ?? `id: ${value}\nsteps:\n  - type: `;
  localError.value = "";
}

function create(): void {
  let index = 1;
  while (
    collection.value.definitions.some(
      (entry) => entry.id === `workflow-${index}`,
    )
  )
    index++;
  select(`workflow-${index}`);
}

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

async function restoreAll(): Promise<void> {
  if (await save(cloneControlPlaneValue(shipped.value)))
    select(shipped.value.definitions[0]?.id ?? "");
}
</script>

<template>
  <ConfigCard title="Definitions">
    <div class="mb-4 flex flex-wrap items-center gap-2">
      <Select
        :model-value="selected"
        @update:model-value="select(String($event))"
      >
        <SelectTrigger class="min-w-56"
          ><SelectValue placeholder="Select workflow"
        /></SelectTrigger>
        <SelectContent
          ><SelectItem
            v-for="entry in collection.definitions"
            :key="entry.id"
            :value="entry.id"
            >{{ entry.id }}</SelectItem
          ></SelectContent
        >
      </Select>
      <Button type="button" variant="outline" @click="create"
        ><Plus /> New</Button
      >
      <Button
        type="button"
        variant="outline"
        :disabled="!shipped.definitions.length"
        @click="restoreAll"
        ><RotateCcw /> Restore shipped</Button
      >
    </div>

    <form class="space-y-4" @submit.prevent="saveDraft">
      <div class="space-y-1.5">
        <Label for="workflow-id">ID</Label>
        <Input
          id="workflow-id"
          v-model="id"
          name="workflow-id"
          required
          autocomplete="off"
        />
      </div>
      <div class="space-y-1.5">
        <Label for="workflow-yaml">YAML</Label>
        <Textarea
          id="workflow-yaml"
          v-model="yaml"
          name="workflow-yaml"
          required
          class="min-h-80 resize-y font-mono"
          spellcheck="false"
        />
      </div>
      <p
        v-if="localError || state.error"
        class="text-sm text-destructive"
        role="alert"
      >
        {{ localError || state.error }}
      </p>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-2">
          <Badge :variant="state.stream === 'live' ? 'ok' : 'warn'">{{
            state.stream === "live" ? "Live" : "Connecting"
          }}</Badge>
          <span v-if="state.resource" class="text-xs text-muted-foreground"
            >Version {{ state.resource.version }}</span
          >
        </div>
        <div class="flex gap-2">
          <Button
            v-if="shippedEntry"
            type="button"
            variant="outline"
            @click="restoreOne"
            ><RotateCcw /> Restore this workflow</Button
          >
          <Button
            v-if="selected"
            type="button"
            variant="destructive"
            @click="remove"
            ><Trash2 /> Remove</Button
          >
          <Button type="submit" :disabled="state.saving"
            ><Spinner v-if="state.saving" data-icon="inline-start" /> Validate &
            save</Button
          >
        </div>
      </div>
    </form>
  </ConfigCard>
</template>
