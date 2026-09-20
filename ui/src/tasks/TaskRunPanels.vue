<script setup lang="ts">
import { TabsContent } from "@/components/ui/tabs";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";

import AttemptConfig from "./AttemptConfig.vue";
import ChangedFiles from "./ChangedFiles.vue";
import DebugView from "./DebugView.vue";
import PanelError from "./PanelError.vue";
import PanelLoading from "./PanelLoading.vue";
import StageRail from "./StageRail.vue";
import TaskLogs from "./TaskLogs.vue";
import { RUN_TABS } from "./task-run";
import { useTaskRun } from "./use-task-run";

/**
 * The run page's panel area: one panel per tab, each reading the attempt the
 * page has selected.
 *
 * The rail owns its own loading, failure and empty states. Every other panel is
 * scoped to one attempt, and "no attempt" is a claim only a successful read of
 * the run history can make -- a read still in flight is not a verdict either,
 * and a failed read is reported as a failed read.
 */
const props = defineProps<{ id: string }>();

const run = useTaskRun();

function retryLogs(): void {
  if (run.attemptNumber != null) run.loadLogs(run.attemptNumber, { force: true });
}

function retryChanges(): void {
  if (run.attemptNumber != null) run.loadChanges(run.attemptNumber, { force: true });
}

function retryDebug(): void {
  if (run.attemptNumber != null) run.loadDebug(run.attemptNumber, { force: true });
}
</script>

<template>
  <TabsContent v-for="tab in RUN_TABS" :key="tab.id" :value="tab.id">
    <StageRail v-if="tab.id === 'stages'" :state="run.attempts" @retry="run.loadAttempts" />

    <template v-else>
      <PanelLoading v-if="run.attempts === undefined" label="Loading this task's attempts…" />

      <PanelError
        v-else-if="run.attempts === null"
        title="Could not load this task's attempts"
        detail="The task's run history could not be read."
        @retry="run.loadAttempts"
      />

      <Empty v-else-if="run.attemptNumber == null">
        <EmptyHeader>
          <EmptyTitle>No attempt recorded</EmptyTitle>
          <EmptyDescription>
            This task has no recorded run.
          </EmptyDescription>
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
        @retry="retryLogs"
      />

      <ChangedFiles v-else-if="tab.id === 'changes'" :state="run.changesState" :task="run.task" @retry="retryChanges" />

      <AttemptConfig
        v-else-if="tab.id === 'config'"
        :events="run.events"
        :attempt="run.attemptNumber"
        @retry="run.loadEvents"
      />

      <DebugView v-else-if="tab.id === 'debug'" :state="run.debugState" :attempt="run.attemptNumber" @retry="retryDebug" />
    </template>
  </TabsContent>
</template>
