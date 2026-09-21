<script setup lang="ts">
import { computed, reactive, watch } from "vue";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { useControlPlaneStore, type ResourceDescriptor } from "@/stores/control-plane";
import ApplyStatusRows from "./ApplyStatusRows.vue";
import ConfigCard from "./ConfigCard.vue";
import ResourceHistory from "./ResourceHistory.vue";

interface JSONSchemaProperty {
  type?: "integer" | "number" | "string" | "boolean";
  title?: string;
  description?: string;
  minimum?: number;
  maximum?: number;
  enum?: Array<string | number>;
}

const props = defineProps<{ descriptor: ResourceDescriptor }>();
const store = useControlPlaneStore();
const state = computed(() => store.stateFor(props.descriptor.kind));
const draft = reactive<Record<string, unknown>>({});
const touched = reactive<Record<string, boolean>>({});

const schema = computed(() => {
  try { return JSON.parse(props.descriptor.schema_json) as { properties?: Record<string, JSONSchemaProperty> }; }
  catch { return {}; }
});
const fields = computed(() => Object.entries(schema.value.properties ?? {}));

watch(() => state.value.resource, (resource) => {
  if (!resource) return;
  for (const key of Object.keys(draft)) delete draft[key];
  Object.assign(draft, resource.value as Record<string, unknown>);
}, { immediate: true });

function label(key: string, property: JSONSchemaProperty): string {
  if (property.title) return property.title;
  const taskLabels: Record<string, string> = {
    max_model_tool_steps: "Max model steps",
    max_runtime_seconds: "Max runtime (seconds)",
    max_consecutive_gate_failures: "Max gate failures",
  };
  return taskLabels[key] ?? key.replaceAll("_", " ").replace(/^./, (value) => value.toUpperCase());
}

function update(key: string, property: JSONSchemaProperty, value: unknown): void {
  touched[key] = true;
  if (property.type === "integer" || property.type === "number") draft[key] = Number(value);
  else if (property.type === "boolean") draft[key] = value === true || value === "true";
  else draft[key] = value;
}

function invalid(key: string, property: JSONSchemaProperty): boolean {
  if (!touched[key]) return false;
  const value = draft[key];
  if ((property.type === "integer" || property.type === "number") && !Number.isFinite(value)) return true;
  if (typeof value === "number" && property.minimum !== undefined && value < property.minimum) return true;
  if (typeof value === "number" && property.maximum !== undefined && value > property.maximum) return true;
  return false;
}

async function save(): Promise<void> {
  for (const [key] of fields.value) touched[key] = true;
  if (fields.value.some(([key, property]) => invalid(key, property))) return;
  if (await store.replace(props.descriptor.kind, { ...draft })) {
    for (const key of Object.keys(touched)) touched[key] = false;
  }
}
</script>

<template>
  <ConfigCard :title="descriptor.title">
    <form class="flex flex-col gap-5" @submit.prevent="save">
      <FieldGroup>
        <Field v-for="[key, property] in fields" :key="key" :data-invalid="invalid(key, property)">
          <FieldLabel :for="`${descriptor.kind}-${key}`">{{ label(key, property) }}</FieldLabel>
          <Select v-if="property.type === 'boolean' || property.enum" :model-value="String(draft[key])" @update:model-value="update(key, property, $event)">
            <SelectTrigger :id="`${descriptor.kind}-${key}`" :aria-invalid="invalid(key, property)"><SelectValue /></SelectTrigger>
            <SelectContent><SelectGroup>
              <SelectItem v-for="option in property.enum ?? ['true', 'false']" :key="String(option)" :value="String(option)">{{ option }}</SelectItem>
            </SelectGroup></SelectContent>
          </Select>
          <Input
            v-else
            :id="`${descriptor.kind}-${key}`"
            :model-value="draft[key] as string | number"
            :type="property.type === 'integer' || property.type === 'number' ? 'number' : 'text'"
            :min="property.minimum"
            :max="property.maximum"
            :step="property.type === 'integer' ? 1 : undefined"
            :aria-invalid="invalid(key, property)"
            autocomplete="off"
            @update:model-value="update(key, property, $event)"
          />
          <FieldDescription v-if="property.description">{{ property.description }}</FieldDescription>
          <FieldDescription v-if="invalid(key, property)" class="text-destructive">Enter a valid value.</FieldDescription>
        </Field>
      </FieldGroup>
      <p v-if="state.error" class="text-sm text-destructive" role="alert">{{ state.error }}</p>
      <div class="flex items-center justify-between gap-3">
        <div class="flex items-center gap-2">
          <Badge :variant="state.stream === 'live' ? 'ok' : 'warn'">{{ state.stream === 'live' ? 'Live' : 'Connecting' }}</Badge>
          <Badge v-if="descriptor.apply_mode === 'restart-required'" variant="warn">Restart required</Badge>
          <span v-if="state.resource" class="text-xs text-muted-foreground">Version {{ state.resource.version }}</span>
        </div>
        <Button type="submit" :disabled="state.saving || state.loading">
          <Spinner v-if="state.saving" data-icon="inline-start" />
          Save changes
        </Button>
      </div>
    </form>
    <ApplyStatusRows :kind="descriptor.kind" />
    <ResourceHistory :kind="descriptor.kind" />
  </ConfigCard>
</template>
