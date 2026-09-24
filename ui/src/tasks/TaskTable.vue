<script setup lang="ts">
import { computed } from "vue";

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import TaskRow, { type Task } from "./TaskRow.vue";

/**
 * The board. It renders the rows it is given and nothing else: which rows are
 * visible, and which state stands in when none are, are the page's decisions.
 * The `state` slot is drawn in the body's place so the columns stay announced
 * while the table has no rows to show.
 */
const props = withDefaults(
  defineProps<{ tasks: Task[]; showRepo?: boolean }>(),
  { showRepo: true },
);

const emit = defineEmits<{ done: [taskId: Task["id"]] }>();

const columns = computed(() => (props.showRepo ? 8 : 7));
</script>

<template>
  <Table>
    <TableHeader>
      <TableRow>
        <TableHead v-if="props.showRepo">Repo</TableHead>
        <TableHead>Issue</TableHead>
        <!-- Title takes the slack: it is the only column that distinguishes one
             row from another, so it should be the one that grows. -->
        <TableHead class="w-full">Title</TableHead>
        <TableHead>Status</TableHead>
        <TableHead>Workflow</TableHead>
        <TableHead>Stage</TableHead>
        <TableHead>Last activity</TableHead>
        <TableHead>Action</TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      <TableRow v-if="!props.tasks.length">
        <TableCell :colspan="columns">
          <slot name="state" />
        </TableCell>
      </TableRow>
      <template v-else>
        <TaskRow
          v-for="task in props.tasks"
          :key="task.id"
          :task="task"
          :show-repo="props.showRepo"
          @done="emit('done', $event)"
        />
      </template>
    </TableBody>
  </Table>
</template>
