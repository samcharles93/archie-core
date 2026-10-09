<script setup lang="ts">
import { computed } from "vue";
import { ArrowDown, ArrowUp, ArrowUpDown } from "@lucide/vue";
import {
  createColumnHelper,
  createSortedRowModel,
  rowSortingFeature,
  sortFn_basic,
  sortFn_text,
  tableFeatures,
  useTable,
} from "@tanstack/vue-table";
import { Button } from "@/components/ui/button";

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import TaskRow, { type Task } from "./TaskRow.vue";

const props = withDefaults(
  defineProps<{ tasks: Task[]; showRepo?: boolean }>(),
  { showRepo: true },
);

const emit = defineEmits<{ done: [taskId: Task["id"]] }>();

const features = tableFeatures({
  rowSortingFeature,
  sortedRowModel: createSortedRowModel(),
  sortFns: { text: sortFn_text, basic: sortFn_basic },
});
const columnHelper = createColumnHelper<typeof features, Task>();
const table = useTable({
  features,
  get data() {
    return props.tasks;
  },
  getRowId: (task: Task) => String(task.id),
  columns: columnHelper.columns([
    columnHelper.accessor((task) => `${task.owner ?? ""}/${task.repo ?? ""}`, {
      id: "repo",
      header: "Repo",
      sortFn: "text",
    }),
    columnHelper.accessor((task) => task.issue_number ?? 0, {
      id: "issue",
      header: "Issue",
      sortFn: "basic",
    }),
    columnHelper.accessor((task) => task.title ?? "untitled task", {
      id: "title",
      header: "Title",
      sortFn: "text",
    }),
    columnHelper.accessor((task) => task.status ?? "", {
      id: "status",
      header: "Status",
      sortFn: "text",
    }),
    columnHelper.accessor((task) => task.workflow ?? "", {
      id: "workflow",
      header: "Workflow",
      sortFn: "text",
    }),
    columnHelper.accessor((task) => Date.parse(task.updated_at ?? "") || 0, {
      id: "activity",
      header: "Last activity",
      sortFn: "basic",
    }),
  ]),
});
const headers = computed(() =>
  table.getAllColumns().filter((column) => props.showRepo || column.id !== "repo"),
);
// shortcut: sorting covers loaded rows; use server sorting if all pages must be ordered.
const rows = computed(() => table.getRowModel().rows);
const columns = computed(() => (props.showRepo ? 7 : 6));
</script>

<template>
  <Table>
    <TableHeader>
      <TableRow>
        <TableHead
          v-for="column in headers"
          :key="column.id"
          :class="column.id === 'title' ? 'w-full' : undefined"
          :aria-sort="
            column.getIsSorted() === 'asc' ? 'ascending' :
            column.getIsSorted() === 'desc' ? 'descending' : 'none'
          "
        >
          <Button variant="ghost" size="sm" @click="column.toggleSorting()">
            {{ column.columnDef.header }}
            <component
              :is="column.getIsSorted() === 'asc' ? ArrowUp : column.getIsSorted() === 'desc' ? ArrowDown : ArrowUpDown"
              data-icon="inline-end"
            />
          </Button>
        </TableHead>
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
          v-for="row in rows"
          :key="row.id"
          :task="row.original"
          :show-repo="props.showRepo"
          @done="emit('done', $event)"
        />
      </template>
    </TableBody>
  </Table>
</template>
