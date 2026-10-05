<script setup lang="ts">
import { computed, ref } from "vue";
import { Plus, Trash2, X } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { statusKind, statusLabel, type Binding } from "@/bindings/binding-draft";
import BindEvent from "./BindEvent.vue";
import { setWorkflowField, workflowField } from "./workflow-edit";
import type { WorkflowTrigger } from "./workflow-triggers";

const props = defineProps<{ yaml: string; triggers: WorkflowTrigger[] }>();
const emit = defineEmits<{ "update:yaml": [string]; close: []; bound: [] }>();

// The bind flow replaces the settings in this panel; null binding is a new one.
const binding = ref<Binding | null | undefined>();
function bound(): void {
  binding.value = undefined;
  emit("bound");
}

type Spec = { type?: string; required?: boolean };
const TYPES = ["string", "number", "bool", "object", "array", "any"];
// Kept out of the template: a literal reference there would close the
// template's own interpolation.
const EMPTY = {
  inputs: "Takes none. Steps read declared inputs as {{ inputs.name }}.",
  outputs: "Writes none. A calling workflow can read declared outputs.",
};

const text = (key: string) => {
  const value = workflowField(props.yaml, key);
  return typeof value === "string" ? value : "";
};
const specs = (key: "inputs" | "outputs") =>
  computed(() => Object.entries((workflowField(props.yaml, key) as Record<string, Spec> | undefined) ?? {}));
const inputs = specs("inputs");
const outputs = specs("outputs");

function set(key: string, value: unknown): void {
  emit("update:yaml", setWorkflowField(props.yaml, key, value));
}
function setSpec(key: "inputs" | "outputs", entries: [string, Spec][]): void {
  set(key, Object.fromEntries(entries.filter(([name]) => name)));
}
function editSpec(key: "inputs" | "outputs", list: [string, Spec][], index: number, name: string, spec: Spec): void {
  setSpec(key, list.map((entry, i) => (i === index ? [name, spec] : entry)));
}
function addSpec(key: "inputs" | "outputs", list: [string, Spec][]): void {
  let n = list.length + 1;
  while (list.some(([name]) => name === `${key.slice(0, -1)}_${n}`)) n++;
  setSpec(key, [...list, [`${key.slice(0, -1)}_${n}`, { type: "string" }]]);
}

const MODES = [
  { value: "required", label: "Repository", hint: "Clones the task's repository" },
  { value: "optional", label: "If given", hint: "Clones one when the run names it" },
  { value: "none", label: "None", hint: "Scratch workspace, no clone" },
];
const repository = computed(() => text("repository") || "required");

// needs is the workflow's declared harness requirements: captures (a stage
// returns structured output through capture tools) and the largest gate-retry
// budget any stage declares.
const needs = computed<{ captures?: boolean; gate_retries?: number }>(() => {
  const value = workflowField(props.yaml, "needs");
  return value && typeof value === "object" ? (value as { captures?: boolean; gate_retries?: number }) : {};
});
function setNeeds(next: { captures?: boolean; gate_retries?: number }): void {
  set("needs", next);
}
</script>

<template>
  <aside class="flex h-full w-[24rem] flex-col border-l border-border bg-card shadow-xl">
    <BindEvent v-if="binding !== undefined" :yaml="yaml" :binding="binding" @back="binding = undefined" @saved="bound" />
    <template v-else>
    <header class="flex items-center gap-2 border-b border-border px-4 py-3">
      <div class="min-w-0 flex-1">
        <div class="text-sm font-medium">Workflow settings</div>
      </div>
      <Button type="button" variant="ghost" size="icon-sm" aria-label="Close" @click="emit('close')"><X /></Button>
    </header>

    <div class="flex-1 space-y-5 overflow-y-auto px-4 py-4">
      <section class="space-y-1.5">
        <div class="text-xs font-medium text-muted-foreground">Works on</div>
        <div class="grid grid-cols-3 gap-1 rounded-md bg-secondary p-1">
          <button
            v-for="mode in MODES"
            :key="mode.value"
            type="button"
            class="rounded px-2 py-1.5 text-[13px] transition-colors"
            :class="repository === mode.value ? 'bg-card shadow-sm' : 'text-muted-foreground hover:text-foreground'"
            :title="mode.hint"
            @click="set('repository', mode.value === 'required' ? undefined : mode.value)"
          >
            {{ mode.label }}
          </button>
        </div>
      </section>

      <section v-for="[key, list] in [['inputs', inputs], ['outputs', outputs]] as const" :key="key" class="space-y-1.5">
        <div class="flex items-center text-xs font-medium text-muted-foreground">
          {{ key === "inputs" ? "Inputs" : "Outputs" }}
          <button type="button" class="ml-auto flex items-center gap-0.5 hover:text-foreground" @click="addSpec(key, list)">
            <Plus class="size-3" /> Add
          </button>
        </div>
        <div v-if="list.length" class="divide-y divide-border rounded-md border border-border">
          <div v-for="([name, spec], i) in list" :key="i" class="flex items-center gap-2 px-2 py-1.5">
            <input
              :value="name"
              class="h-7 min-w-0 flex-1 rounded-md border border-input bg-background px-2 font-mono text-xs"
              :aria-label="`${key} name`"
              @change="editSpec(key, list, i, ($event.target as HTMLInputElement).value.trim(), spec)"
            />
            <select
              :value="spec.type ?? 'string'"
              class="h-7 rounded-md border border-input bg-background px-1 text-xs"
              @change="editSpec(key, list, i, name, { ...spec, type: ($event.target as HTMLSelectElement).value })"
            >
              <option v-for="type in TYPES" :key="type" :value="type">{{ type }}</option>
            </select>
            <Switch
              :model-value="spec.required === true"
              :title="spec.required ? 'Required' : 'Optional'"
              @update:model-value="editSpec(key, list, i, name, { ...spec, required: $event || undefined })"
            />
            <button type="button" class="text-muted-foreground hover:text-danger" :aria-label="`Remove ${name}`" @click="setSpec(key, list.filter((_: unknown, j: number) => j !== i))">
              <Trash2 class="size-3.5" />
            </button>
          </div>
        </div>
        <p v-else class="text-xs text-fg-subtle">
          {{ EMPTY[key] }}
        </p>
      </section>

      <section class="space-y-1.5">
        <div class="text-xs font-medium text-muted-foreground">Runs as</div>
        <div class="divide-y divide-border rounded-md border border-border">
          <label class="flex min-h-10 items-center gap-3 px-3 py-1.5">
            <span class="flex-1 text-[13px]">Identity</span>
            <input
              :value="text('identity')"
              placeholder="whoever starts it"
              class="h-7 w-44 rounded-md border border-input bg-background px-2 text-[13px]"
              @change="set('identity', ($event.target as HTMLInputElement).value.trim())"
            />
          </label>
          <label class="flex min-h-10 items-center gap-3 px-3 py-1.5">
            <span class="flex-1 text-[13px]">Container profile</span>
            <input
              :value="text('profile')"
              placeholder="default"
              class="h-7 w-44 rounded-md border border-input bg-background px-2 text-[13px]"
              @change="set('profile', ($event.target as HTMLInputElement).value.trim())"
            />
          </label>
        </div>
      </section>

      <section class="space-y-1.5">
        <div class="text-xs font-medium text-muted-foreground">Run needs</div>
        <div class="divide-y divide-border rounded-md border border-border">
          <label class="flex min-h-10 items-center gap-3 px-3 py-1.5">
            <span class="flex-1 text-[13px]" title="A stage returns structured output through capture tools.">Captures</span>
            <Switch :model-value="needs.captures === true" @update:model-value="setNeeds({ ...needs, captures: $event || undefined })" />
          </label>
          <label class="flex min-h-10 items-center gap-3 px-3 py-1.5">
            <span class="flex-1 text-[13px]" title="The largest gate-retry budget any stage declares.">Gate retries</span>
            <input
              type="number"
              min="0"
              placeholder="0"
              class="h-7 w-20 rounded-md border border-input bg-background px-2 text-right text-[13px]"
              :value="needs.gate_retries ?? ''"
              @input="setNeeds({ ...needs, gate_retries: ($event.target as HTMLInputElement).value === '' ? undefined : Number(($event.target as HTMLInputElement).value) })"
            />
          </label>
        </div>
      </section>

      <section class="space-y-1.5">
        <div class="flex items-center text-xs font-medium text-muted-foreground">
          Starts when
          <button type="button" class="ml-auto flex items-center gap-0.5 hover:text-foreground" @click="binding = null"><Plus class="size-3" /> Bind an event</button>
        </div>
        <ul v-if="triggers.length" class="divide-y divide-border rounded-md border border-border">
          <li v-for="trigger in triggers" :key="trigger.kind + trigger.label" class="text-[13px]">
            <button v-if="trigger.binding" type="button" class="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-secondary" @click="binding = trigger.binding">
              <span class="truncate">{{ trigger.label }}</span>
              <Badge v-if="trigger.binding.status !== 'armed'" :variant="statusKind(trigger.binding.status)">{{ statusLabel(trigger.binding.status) }}</Badge>
              <span v-if="trigger.detail" class="ml-auto truncate text-xs text-fg-subtle">{{ trigger.detail }}</span>
            </button>
            <div v-else class="flex items-center gap-2 px-3 py-2">
              <span class="truncate">{{ trigger.label }}</span>
              <span v-if="trigger.detail" class="ml-auto truncate text-xs text-fg-subtle">{{ trigger.detail }}</span>
            </div>
          </li>
        </ul>
        <p v-else class="text-xs text-fg-subtle">Nothing starts it automatically.</p>
      </section>
    </div>
    </template>
  </aside>
</template>
