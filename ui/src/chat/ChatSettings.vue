<script setup lang="ts">
import { Settings } from "@lucide/vue";
import { computed, ref } from "vue";

import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  currentModels,
  selectedModel,
  selectedPersona,
  selectedProvider,
  selectorData,
  setModel,
  setPersona,
  setProvider,
  statusText,
  useEscapeLayer,
} from "./state";

/**
 * The chat's own settings, folded away: what the panel is doing right now, and
 * the three selectors the server offers.
 *
 * A Popover rather than a disclosure menu: it closes on an outside pointerdown
 * and on focus leaving it, which is what a control that opens over the
 * transcript has to do. It portals out of the panel, so the panel's overflow
 * cannot clip it.
 *
 * reka-ui refuses an empty string as an item value (that is how it spells
 * "nothing selected") while the wire reads an absent value as "use the
 * default", so the sentinel converts at this boundary and the state below keeps
 * the empty string.
 */
const DEFAULT = "__default__";

const open = ref(false);
useEscapeLayer(open);

const personaValue = computed(() => selectedPersona.value || DEFAULT);
const providerValue = computed(() => selectedProvider.value || DEFAULT);
const modelValue = computed(() => selectedModel.value || DEFAULT);

function clear(value: unknown): string {
  return value === DEFAULT ? "" : String(value ?? "");
}
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child>
      <Button variant="ghost" size="icon-sm" aria-label="Chat settings" title="Chat settings">
        <Settings />
      </Button>
    </PopoverTrigger>
    <PopoverContent align="end" class="grid w-64 gap-3">
      <span class="text-xs text-fg-subtle">{{ statusText }}</span>

      <Field class="gap-1">
        <FieldLabel class="text-xs text-fg-muted">Personality</FieldLabel>
        <Select :model-value="personaValue" @update:model-value="setPersona(clear($event))">
          <SelectTrigger size="sm" class="w-full" aria-label="Personality">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem :value="DEFAULT">Default</SelectItem>
              <SelectItem v-for="name in selectorData.personas || []" :key="name" :value="name">{{ name }}</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </Field>

      <Field v-if="(selectorData.providers || []).length" class="gap-1">
        <FieldLabel class="text-xs text-fg-muted">Provider</FieldLabel>
        <Select :model-value="providerValue" @update:model-value="setProvider(clear($event))">
          <SelectTrigger size="sm" class="w-full" aria-label="Provider">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem :value="DEFAULT">Default</SelectItem>
              <SelectItem v-for="provider in selectorData.providers || []" :key="provider" :value="provider">
                {{ provider }}
              </SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </Field>

      <Field class="gap-1">
        <FieldLabel class="text-xs text-fg-muted">Model</FieldLabel>
        <Select :model-value="modelValue" @update:model-value="setModel(clear($event))">
          <SelectTrigger size="sm" class="w-full" aria-label="Model">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem :value="DEFAULT">Default</SelectItem>
              <SelectItem v-for="model in currentModels" :key="model" :value="model">{{ model }}</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </Field>
    </PopoverContent>
  </Popover>
</template>
