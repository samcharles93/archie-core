<script setup lang="ts">
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { ConfigFieldType } from "./types";

/**
 * The editor for one config field: a text input, or a select for the two types
 * whose value is a closed set.
 *
 * It owns focus -- the row mounts it the moment the row enters edit mode, so
 * the first keystroke lands in the control -- and reports Enter and Escape.
 * Saving and cancelling belong to the row, which is what knows whether what
 * was typed can be sent.
 *
 * Enter commits for the text input only. reka's select opens its listbox on
 * Enter, so a select commits through its own listbox and the row's Save.
 */
const props = defineProps<{
  modelValue: string;
  type: ConfigFieldType;
  options?: string[];
  disabled?: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [string];
  save: [];
  cancel: [];
}>();

const BOOL_OPTIONS = ["true", "false"];

function update(value: unknown): void {
  emit("update:modelValue", String(value ?? ""));
}

const vFocus = {
  mounted: (el: HTMLElement) => {
    el.focus();
  },
};
</script>

<template>
  <Select
    v-if="props.type === 'bool' || props.type === 'enum'"
    :model-value="props.modelValue"
    :disabled="props.disabled"
    @update:model-value="update"
  >
    <SelectTrigger v-focus class="w-full" :aria-label="props.type === 'bool' ? 'Value' : 'Option'">
      <SelectValue />
    </SelectTrigger>
    <SelectContent>
      <SelectGroup>
        <SelectItem v-for="option in props.type === 'bool' ? BOOL_OPTIONS : props.options || []" :key="option" :value="option">
          {{ option }}
        </SelectItem>
      </SelectGroup>
    </SelectContent>
  </Select>
  <Input
    v-else
    v-focus
    :model-value="props.modelValue"
    :disabled="props.disabled"
    autocomplete="off"
    @update:model-value="update"
    @keydown.enter.prevent="emit('save')"
    @keydown.esc="emit('cancel')"
  />
</template>
