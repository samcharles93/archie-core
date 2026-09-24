<script setup lang="ts">
import { Check, Pencil, Trash2 } from "@lucide/vue";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TableCell, TableRow } from "@/components/ui/table";
import BindingStatusBadge from "./BindingStatusBadge.vue";
import { matcherSource, repoPin, type Binding } from "./binding-draft";

/** One saved binding's summary row. */
const props = defineProps<{ binding: Binding }>();
const emit = defineEmits<{
  edit: [binding: Binding];
  approve: [binding: Binding];
  delete: [binding: Binding];
}>();
</script>

<template>
  <TableRow>
    <TableCell class="font-medium">{{ props.binding.name }}</TableCell>
    <TableCell class="font-mono text-fg-muted">
      <div class="flex items-center gap-2">
        {{ matcherSource(props.binding) }}
        <Badge v-if="props.binding.unsigned" variant="warn">Unsigned</Badge>
      </div>
    </TableCell>
    <TableCell>{{ props.binding.workflow || "—" }}</TableCell>
    <TableCell class="font-mono text-fg-muted">{{ repoPin(props.binding) }}</TableCell>
    <TableCell><BindingStatusBadge :status="props.binding.status" /></TableCell>
    <TableCell>
      <div class="flex items-center justify-end gap-1">
        <Button variant="outline" size="sm" @click="emit('edit', props.binding)">
          <Pencil data-icon="inline-start" />
          Edit
        </Button>
        <!-- Only a pending binding can be approved. An armed binding is
             already firing, so the button would be a no-op -- or worse, a
             misleading one. -->
        <Button v-if="props.binding.status === 'pending_approval'" size="sm" @click="emit('approve', props.binding)">
          <Check data-icon="inline-start" />
          Approve
        </Button>
        <Button variant="outline" size="sm" @click="emit('delete', props.binding)">
          <Trash2 data-icon="inline-start" />
          Delete
        </Button>
      </div>
    </TableCell>
  </TableRow>
</template>
