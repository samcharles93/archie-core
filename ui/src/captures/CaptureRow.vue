<script setup lang="ts">
import { ChevronRight } from "@lucide/vue";
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import { TableCell, TableRow } from "@/components/ui/table";
import { ago } from "@/lib/format";
import { captureSignature } from "./capture-signature";
import { selected, type Capture } from "./state";

/**
 * One captured event. The row is the control, not a button sitting in one
 * cell: selecting is the row's whole job, and a keyboard user has to reach what
 * a mouse user clicks -- which is why it is focusable and both Enter and Space
 * select it.
 */
const props = defineProps<{ capture: Capture }>();

const signature = computed(() => captureSignature(props.capture));
const isSelected = computed(() => selected.value?.id === props.capture.id);

// The name is set here because role="button" makes the cells presentational:
// what a screen reader reads is this, not five unrelated columns.
const label = computed(() => `Show the payload received from ${props.capture.source || "an unknown source"}`);

function open(): void {
  selected.value = props.capture;
}
</script>

<template>
  <TableRow
    :data-state="isSelected ? 'selected' : undefined"
    role="button"
    tabindex="0"
    :aria-current="isSelected ? 'true' : undefined"
    :aria-label="label"
    class="cursor-pointer focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
    @click="open"
    @keydown.enter.prevent="open"
    @keydown.space.prevent="open"
  >
    <TableCell class="font-mono">{{ props.capture.source || "(unknown)" }}</TableCell>
    <TableCell :title="props.capture.received_at || ''">{{ ago(props.capture.received_at) }}</TableCell>
    <!-- A stored capture carries no claim from a binding, so this reads Unbound
         for every row: the column is where a claim would appear, not a state a
         reader can act on today. -->
    <TableCell>
      <div class="flex items-center gap-1">
        <Badge variant="idle">Unbound</Badge>
        <Badge :variant="signature.kind">{{ signature.label }}</Badge>
      </div>
    </TableCell>
    <TableCell class="font-mono">{{ props.capture.content_type || "—" }}</TableCell>
    <TableCell class="text-right">
      <ChevronRight class="size-4 text-fg-subtle" />
    </TableCell>
  </TableRow>
</template>
