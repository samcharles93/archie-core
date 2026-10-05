<script setup lang="ts">
import { computed } from "vue";
import { Check, Pencil, Trash2 } from "@lucide/vue";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TableCell, TableRow } from "@/components/ui/table";
import BindingStatusBadge from "./BindingStatusBadge.vue";
import { eventTypeLabel, type EventType } from "@/captures/event-types";
import {
  bindingEventType,
  repoPin,
  type Binding,
  type MappingOption,
} from "./binding-draft";

/** One saved binding's summary row. */
const props = defineProps<{
  binding: Binding;
  mappings: MappingOption[];
  eventTypes: EventType[];
}>();
// A filtered capture is the filter doing its job; any other reason means the
// binding matched an event and could not start its workflow.
const failing = computed(() => {
  const reason = props.binding.last_outcome?.reason;
  return !!reason && reason !== "filtered";
});
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
        {{
          eventTypeLabel(
            bindingEventType(props.binding, props.mappings),
            props.eventTypes,
          )
        }}
        <Badge v-if="props.binding.unsigned" variant="warn">Unsigned</Badge>
      </div>
    </TableCell>
    <TableCell
      class="max-w-56 truncate font-mono text-xs text-fg-muted"
      :title="props.binding.filter"
    >
      {{ props.binding.filter || "—" }}
    </TableCell>
    <TableCell>
      <div class="flex items-center gap-2">
        {{ props.binding.workflow || "—" }}
        <Badge v-if="props.binding.workflow_disabled" variant="warn">Disabled</Badge>
        <Badge
          v-if="failing"
          variant="danger"
          :title="props.binding.last_outcome?.reason"
          >Not dispatching</Badge
        >
      </div>
    </TableCell>
    <TableCell class="font-mono text-fg-muted">{{
      repoPin(props.binding)
    }}</TableCell>
    <TableCell><BindingStatusBadge :status="props.binding.status" /></TableCell>
    <TableCell>
      <div class="flex items-center justify-end gap-1">
        <Button
          variant="outline"
          size="sm"
          @click="emit('edit', props.binding)"
        >
          <Pencil data-icon="inline-start" />
          Edit
        </Button>
        <!-- Only a pending binding can be approved. An armed binding is
             already firing, so the button would be a no-op -- or worse, a
             misleading one. -->
        <Button
          v-if="props.binding.status === 'pending_approval'"
          size="sm"
          @click="emit('approve', props.binding)"
        >
          <Check data-icon="inline-start" />
          Approve
        </Button>
        <Button
          variant="outline"
          size="sm"
          @click="emit('delete', props.binding)"
        >
          <Trash2 data-icon="inline-start" />
          Delete
        </Button>
      </div>
    </TableCell>
  </TableRow>
</template>
