<script setup lang="ts">
import { Button } from "@/components/ui/button";
import { TableCell, TableRow } from "@/components/ui/table";
import { eventTypeLabel } from "@/captures/event-types";
import { ago } from "@/lib/format";
import { matchCountLabel } from "./match-count";
import { eventTypes, type Mapping } from "./state";

/** One saved mapping's summary row. Both of its actions are the page's, not the
 * row's: editing opens the editor overlay, deleting opens the confirmation. */
defineProps<{ mapping: Mapping }>();
const emit = defineEmits<{ edit: [mapping: Mapping]; delete: [mapping: Mapping] }>();
</script>

<template>
  <TableRow>
    <TableCell class="font-medium">{{ mapping.name }}</TableCell>
    <TableCell class="font-mono text-fg-muted">{{ eventTypeLabel(mapping.event_type_id, eventTypes) }}</TableCell>
    <TableCell class="tabular-nums">{{ mapping.fields?.length ?? 0 }}</TableCell>
    <TableCell class="tabular-nums">{{ matchCountLabel(mapping) }}</TableCell>
    <TableCell class="text-fg-muted">{{ mapping.match_count ? ago(mapping.last_matched_at) : "—" }}</TableCell>
    <TableCell>
      <div class="flex justify-end gap-2">
        <Button variant="outline" size="xs" @click="emit('edit', mapping)">Edit</Button>
        <Button variant="destructive" size="xs" @click="emit('delete', mapping)">Delete</Button>
      </div>
    </TableCell>
  </TableRow>
</template>
