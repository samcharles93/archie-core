<script setup lang="ts">
import { AlertTriangle } from "@lucide/vue";
import { computed } from "vue";

import { Alert, AlertDescription } from "@/components/ui/alert";

import { useTaskRun } from "./use-task-run";

/**
 * Two things the page can say about its own reads, both warnings rather than
 * errors: the page still works, with less than it would like.
 *
 * The task list is the 100 most recently updated, so a task outside it is
 * normal, and saying so is what stops an operator reading the missing status
 * and controls as the task having none.
 */
const props = defineProps<{ id: string }>();

const run = useTaskRun();

const note = computed(() => {
  if (run.taskList === null) {
    return "The task list could not be read, so this page cannot show the task's current status or its operator controls. The run history below comes from the task's own endpoints.";
  }
  if (Array.isArray(run.taskList) && !run.task) {
    return `Task ${props.id} is not among the tasks this dashboard lists (the list covers the 100 most recently updated). Its recorded attempts are shown below; the Debug tab holds the stored record verbatim.`;
  }
  return "";
});
</script>

<template>
  <!-- warn-soft rather than the Alert's default surface: this is a caution the
       operator should read past, not a panel to look at. -->
  <Alert v-if="note" class="mb-4 border-warn bg-warn-soft">
    <AlertTriangle />
    <AlertDescription>{{ note }}</AlertDescription>
  </Alert>
</template>
