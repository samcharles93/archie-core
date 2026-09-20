<script setup lang="ts">
import { computed } from "vue";

import { TableCell, TableRow } from "@/components/ui/table";
import { ago } from "@/lib/format";
import { activityDetail } from "./activity-detail";
import { taskIDFor, type ActivityEvent } from "./state";

/**
 * One row of the live activity table. A row whose event resolves to a task is
 * a link to it, and has to behave like one for a keyboard: Enter and Space both
 * open it, which is why it is focusable and carries role="link".
 */
const props = defineProps<{ event: ActivityEvent }>();
const emit = defineEmits<{ open: [taskID: number] }>();

const taskID = computed(() => taskIDFor(props.event));
// The detail is cut to one line by activityDetail and clamped again in the
// cell so a long unbroken token (a path, a URL, a hash) cannot widen the column
// past the table. Unclamped, this made the dashboard 7,481px tall.
const detail = computed(() => activityDetail(props.event));

function open() {
  if (taskID.value > 0) emit("open", taskID.value);
}
</script>

<template>
  <TableRow
    :class="taskID > 0 ? 'cursor-pointer' : ''"
    :role="taskID > 0 ? 'link' : undefined"
    :tabindex="taskID > 0 ? 0 : undefined"
    :title="taskID > 0 ? 'Open task details' : undefined"
    @click="open"
    @keydown.enter.prevent="open"
    @keydown.space.prevent="open"
  >
    <TableCell class="font-medium">{{ props.event.kind || props.event.type || "event" }}</TableCell>
    <TableCell class="font-mono">{{ taskID > 0 ? `#${taskID}` : "—" }}</TableCell>
    <TableCell class="w-[55%] max-w-0">
      <span class="block truncate" :title="detail.truncated ? detail.full : undefined">{{ detail.text }}</span>
    </TableCell>
    <TableCell>{{ ago(props.event.at || Date.now()) }}</TableCell>
  </TableRow>
</template>
