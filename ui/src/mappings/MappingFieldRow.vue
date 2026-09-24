<script setup lang="ts">
import { Trash2 } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { TableCell, TableRow } from "@/components/ui/table";
import { FIELD_TYPES, asFieldType } from "./mapping-fields";
import MappingPreviewStatus from "./MappingPreviewStatus.vue";
import type { MappingField, Preview, PreviewFailure } from "./state";

/**
 * One bound field: the name the workflow receives, the path it resolves from,
 * the shape it claims, and whether it must resolve at all.
 *
 * The path is shown but not editable. It is set by clicking a value in the
 * payload, so a path that does not exist in the captured event cannot be typed
 * in by hand -- which is the whole point of authoring against a real one.
 *
 * The type is a Select rather than a ToggleGroup: six options do not fit a
 * table cell at 720px, and a segmented control there would push the row wider
 * than the dialog it sits in.
 */
defineProps<{
  field: MappingField;
  preview: Preview | null;
  failure?: PreviewFailure;
}>();
const emit = defineEmits<{
  update: [patch: Partial<MappingField>];
  remove: [];
}>();
</script>

<template>
  <TableRow>
    <TableCell>
      <Input
        :model-value="field.name"
        aria-label="Field name"
        @update:model-value="(value) => emit('update', { name: String(value) })"
      />
    </TableCell>
    <TableCell class="font-mono text-xs">
      <span class="block max-w-56 truncate" :title="field.path">{{
        field.path
      }}</span>
    </TableCell>
    <TableCell>
      <Select
        :model-value="field.type"
        @update:model-value="
          (value) => emit('update', { type: asFieldType(value) })
        "
      >
        <SelectTrigger size="sm" :aria-label="`Type for ${field.name}`">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem v-for="type in FIELD_TYPES" :key="type" :value="type">{{
              type
            }}</SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
    </TableCell>
    <TableCell>
      <Checkbox
        :model-value="field.required"
        :aria-label="`Required: ${field.name}`"
        @update:model-value="
          (value) => emit('update', { required: value === true })
        "
      />
    </TableCell>
    <TableCell>
      <MappingPreviewStatus
        :field="field"
        :preview="preview"
        :failure="failure"
      />
    </TableCell>
    <TableCell>
      <Button
        variant="ghost"
        size="icon-xs"
        :aria-label="`Remove ${field.name}`"
        @click="emit('remove')"
      >
        <Trash2 />
      </Button>
    </TableCell>
  </TableRow>
</template>
