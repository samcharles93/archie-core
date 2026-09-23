<script setup lang="ts">
import { TabsContent } from "@/components/ui/tabs";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";

import AttemptConfig from "./AttemptConfig.vue";
import ChangedFiles from "./ChangedFiles.vue";
import DebugView from "./DebugView.vue";
import PanelError from "./PanelError.vue";
import PanelLoading from "./PanelLoading.vue";
import TaskLogs from "./TaskLogs.vue";
import { RUN_TABS } from "./task-run";
import { useTaskRun } from "./use-task-run";

/**
 * The run page's inspector panels: one panel per tab, each reading the attempt
 * the page has selected. The stage rail lives in the page's master pane and is
 * not one of these tabs.
 *
 * Every panel is scoped to one attempt, and "no attempt" is a claim only a
 * successful read of the run history can make -- a read still in flight is not
 * a verdict either, and a failed read is reported as a failed read.
 */
const props = defineProps<{ id: string }>();

const run = useTaskRun();
</script>

<template>
  <TabsContent v-for="tab in RUN_TABS" :key="tab.id" :value="tab.id">
    <PanelLoading v-if="run.attempts === undefined" label="Loading this task's attempts…" />

    <PanelError
      v-else-if="run.attempts === null"
      title="Could not load this task's attempts"
      detail="The task's run history could not be read."
    />

    <Empty v-else-if="run.attemptNumber == null">
      <EmptyHeader>
        <EmptyTitle>No attempt recorded</EmptyTitle>
      </EmptyHeader>
    </Empty>
    <TaskLogs
      v-else-if="tab.id === 'log'"
      :state="run.logState"
      :attempt="run.attemptNumber"
      :stages="run.stageNames"
      :filters="run.filters"
      :task-id="props.id"
      @filter="run.setFilters"
    />

    <ChangedFiles v-else-if="tab.id === 'changes'" :state="run.changesState" :task="run.task" />

    <AttemptConfig
      v-else-if="tab.id === 'config'"
      :events="run.events"
      :attempt="run.attemptNumber"
    />

    <DebugView v-else-if="tab.id === 'debug'" :state="run.debugState" :attempt="run.attemptNumber" />
  </TabsContent>
</template>
