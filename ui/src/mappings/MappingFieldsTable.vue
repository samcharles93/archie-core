<script setup lang="ts">
import { computed } from "vue";

import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import MappingFieldRow from "./MappingFieldRow.vue";
import { draft, removeField, updateField, type PreviewFailure } from "./state";

/**
 * The fields bound so far, each with what the last preview said about it.
 *
 * With nothing bound there is no table to show, and the next move is in the
 * payload panel, so the empty state points there rather than presenting an
 * empty grid.
 */
const fields = computed(() => draft.value.fields);
const preview = computed(() => draft.value.preview);

/** Preview failures are keyed by field name: the daemon reports a failure by
 * name, and a field is what the operator renamed. */
const failureByField = computed(
  () =>
    new Map<string, PreviewFailure>(
      (preview.value?.failures || []).map((failure) => [
        failure.field_name,
        failure,
      ]),
    ),
);
</script>

<template>
  <p v-if="!fields.length" class="py-3 text-sm text-fg-muted">
    Click a value in the payload to bind a field.
  </p>

  <Table v-else>
    <TableHeader>
      <TableRow>
        <TableHead>Field name</TableHead>
        <TableHead>Path</TableHead>
        <TableHead>Type</TableHead>
        <TableHead>Required</TableHead>
        <TableHead>Preview</TableHead>
        <TableHead><span class="sr-only">Actions</span></TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      <MappingFieldRow
        v-for="(field, index) in fields"
        :key="index"
        :field="field"
        :preview="preview"
        :failure="failureByField.get(field.name)"
        @update="(patch) => updateField(index, patch)"
        @remove="removeField(index)"
      />
    </TableBody>
  </Table>
</template>
