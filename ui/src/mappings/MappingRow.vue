<script setup lang="ts">
import { Button } from "@/components/ui/button";
import { TableCell, TableRow } from "@/components/ui/table";
import type { Mapping } from "./state";

/** One saved mapping's summary row. Both of its actions are the page's, not the
 * row's: editing opens the editor overlay, deleting opens the confirmation. */
defineProps<{ mapping: Mapping }>();
const emit = defineEmits<{ edit: [mapping: Mapping]; delete: [mapping: Mapping] }>();
</script>

<template>
  <TableRow>
    <TableCell class="font-medium">{{ mapping.name }}</TableCell>
    <TableCell class="font-mono">{{ mapping.source_hint || "—" }}</TableCell>
    <TableCell>{{ mapping.fields?.length ?? 0 }}</TableCell>
    <TableCell>
      <div class="flex justify-end gap-2">
        <Button variant="outline" size="xs" @click="emit('edit', mapping)">Edit</Button>
        <Button variant="destructive" size="xs" @click="emit('delete', mapping)">Delete</Button>
      </div>
    </TableCell>
  </TableRow>
</template>
