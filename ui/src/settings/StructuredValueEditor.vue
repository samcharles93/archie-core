<script setup lang="ts">
import { computed } from "vue";
import { Plus, Trash2 } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

const props = withDefaults(defineProps<{
  modelValue: unknown;
  path?: string;
  locked?: boolean;
}>(), { path: "value", locked: false });
const emit = defineEmits<{ "update:modelValue": [value: unknown] }>();

const isArray = computed(() => Array.isArray(props.modelValue));
const isObject = computed(() => props.modelValue !== null && typeof props.modelValue === "object" && !isArray.value);
const entries = computed(() => Object.entries((props.modelValue ?? {}) as Record<string, unknown>));
const readOnly = computed(() => props.locked || /(^|\.)(has_credential|credential_configured|headers_configured)$/.test(props.path));

function label(value: string): string {
  return value.replaceAll("_", " ").replace(/^./, (character) => character.toUpperCase());
}

function setObject(key: string, value: unknown): void {
  emit("update:modelValue", { ...(props.modelValue as Record<string, unknown>), [key]: value });
}

function renameObject(oldKey: string, newKey: string): void {
  const current = { ...(props.modelValue as Record<string, unknown>) };
  const value = current[oldKey];
  delete current[oldKey];
  current[newKey] = value;
  emit("update:modelValue", current);
}

function removeObject(key: string): void {
  const current = { ...(props.modelValue as Record<string, unknown>) };
  delete current[key];
  emit("update:modelValue", current);
}

function addObject(): void {
  const current = props.modelValue as Record<string, unknown>;
  let index = 1;
  while (`new_${index}` in current) index++;
  emit("update:modelValue", { ...current, [`new_${index}`]: "" });
}

function setArray(index: number, value: unknown): void {
  const current = [...(props.modelValue as unknown[])];
  current[index] = value;
  emit("update:modelValue", current);
}

function addArray(): void {
  const current = props.modelValue as unknown[];
  const sample = current[0];
  let value: unknown = Array.isArray(sample) ? [] : sample && typeof sample === "object" ? {} : "";
  if (props.path === "repositories") value = {
    owner: "", name: "", base: "main", gate: [], protect: [], ecosystem: "go", preflight: [], test_glob: "",
    persistent_storage: false, max_retries: 0, allow_concurrent: false, review_enabled: false,
  };
  else if (props.path.endsWith("mcp_servers")) value = {
    name: "", transport: "stdio", command: "", args: [], work_dir: "", url: "",
    sse_endpoint: "", message_endpoint: "", headers_configured: false,
  };
  else if (props.path.endsWith("gate") || props.path.endsWith("preflight")) value = [];
  else if (props.path.endsWith("allowed_user_ids")) value = 0;
  else if (props.path.endsWith("personas")) value = { name: "", prompt: "" };
  else if (props.path === "schedules") value = {
    id: "", pool: "sequential", detail: "", kind: "workflow",
    schedule: { kind: "interval", interval: 3600000000000, start: "0001-01-01T00:00:00Z" },
    target: {}, payload: { text: "" }, next_run: "0001-01-01T00:00:00Z",
    created: "0001-01-01T00:00:00Z", updated: "0001-01-01T00:00:00Z",
  };
  emit("update:modelValue", [...current, value]);
}

function removeArray(index: number): void {
  emit("update:modelValue", (props.modelValue as unknown[]).filter((_, itemIndex) => itemIndex !== index));
}

function scalar(value: string | number): void {
  emit("update:modelValue", typeof props.modelValue === "number" ? Number(value) : value);
}
</script>

<template>
  <div v-if="isArray" class="space-y-2 rounded-lg border border-border/70 p-3">
    <div v-for="(item, index) in modelValue as unknown[]" :key="index" class="flex items-start gap-2">
      <div class="min-w-0 flex-1">
        <StructuredValueEditor :model-value="item" :path="`${path}.${index}`" @update:model-value="setArray(index, $event)" />
      </div>
      <Button type="button" variant="ghost" size="icon-sm" :aria-label="`Remove ${label(path)} row ${index + 1}`" @click="removeArray(index)">
        <Trash2 />
      </Button>
    </div>
    <Button type="button" variant="outline" size="sm" @click="addArray"><Plus /> Add row</Button>
  </div>

  <fieldset v-else-if="isObject" class="space-y-4 rounded-lg border border-border/70 p-3">
    <div v-for="[key, value] in entries" :key="key" class="space-y-1.5">
      <div class="flex items-center gap-2">
        <Label :for="`${path}.${key}`" class="flex-1">{{ label(key) }}</Label>
        <template v-if="path.endsWith('_map') || path.endsWith('labels') || path === 'providers' || path === 'roles'">
          <Input class="h-7 max-w-48" :model-value="key" aria-label="Entry key" @change="renameObject(key, ($event.target as HTMLInputElement).value)" />
          <Button type="button" variant="ghost" size="icon-sm" :aria-label="`Remove ${key}`" @click="removeObject(key)"><Trash2 /></Button>
        </template>
      </div>
      <StructuredValueEditor :id="`${path}.${key}`" :model-value="value" :path="`${path}.${key}`" @update:model-value="setObject(key, $event)" />
    </div>
    <Button v-if="path.endsWith('_map') || path.endsWith('labels') || path === 'providers' || path === 'roles'" type="button" variant="outline" size="sm" @click="addObject"><Plus /> Add entry</Button>
  </fieldset>

  <div v-else-if="typeof modelValue === 'boolean'" class="flex min-h-9 items-center gap-2">
    <Checkbox :id="path" :model-value="modelValue" :disabled="readOnly" @update:model-value="emit('update:modelValue', $event === true)" />
    <span v-if="readOnly" class="text-xs text-muted-foreground">Reported by Archie</span>
  </div>
  <Textarea v-else-if="path.endsWith('.prompt')" :id="path" :model-value="modelValue as string" :readonly="readOnly" autocomplete="off" @update:model-value="scalar" />
  <Input v-else :id="path" :model-value="modelValue as string | number" :type="typeof modelValue === 'number' ? 'number' : 'text'" :readonly="readOnly" autocomplete="off" @update:model-value="scalar" />
</template>
