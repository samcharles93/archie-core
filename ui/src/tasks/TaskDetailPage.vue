<script setup lang="ts">
import { computed } from "vue";
import { useRoute } from "vue-router";

import { Card, CardContent } from "@/components/ui/card";
import { Tabs } from "@/components/ui/tabs";

import AttemptSelector from "./AttemptSelector.vue";
import TabBar from "./TabBar.vue";
import TaskDetailNotice from "./TaskDetailNotice.vue";
import TaskHeader from "./TaskHeader.vue";
import TaskMetaBar from "./TaskMetaBar.vue";
import TaskNotes from "./TaskNotes.vue";
import TaskRunPanels from "./TaskRunPanels.vue";
import StageRail from "./StageRail.vue";
import { parseTaskId } from "./task-run";
import { provideTaskRun } from "./use-task-run";

/**
 * The per-task run detail page, reached at /tasks/:id.
 *
 * One page, one task: which run it is showing, what each stage of that run did,
 * its log, what it changed, the configuration it ran under, and the raw record.
 * The task id rides in the path and the view state (`tab`, `attempt`) rides in
 * the query string, so every view of this page is a deep link that survives a
 * reload -- /tasks/42?tab=changes&attempt=2.
 *
 * The page is a master-detail split pane (docs/prds/task-run-master-detail.md):
 * the stage rail is the master and is always visible, the four context panels
 * are the inspector and own the tab bar. Below the shell's 900px frame
 * breakpoint the rail stacks above the inspector.
 *
 * This file composes the page and nothing else: the reads and the caches are
 * use-task-run, the head, meta bar, notes, restart control, attempt selector,
 * rail, tab bar and panel area are each their own component. The router keys
 * this component on the path (App.vue), so a different :id gets a fresh
 * instance rather than diffing the last task's state in place.
 */
const route = useRoute();

const rawId = computed(() => String(route.params.id ?? ""));
const taskId = computed(() => parseTaskId(rawId.value));

const run = provideTaskRun(taskId);
</script>

<template>
  <TaskDetailNotice v-if="taskId === null" kind="invalid" :raw="rawId" />
  <TaskDetailNotice v-else-if="run.missing" kind="missing" :raw="rawId" />
  <div v-else>
    <TaskHeader :id="rawId" />
    <TaskMetaBar>
      <AttemptSelector class="ml-auto" />
    </TaskMetaBar>
    <TaskNotes :id="rawId" />

    <div class="grid items-start gap-4 min-[900px]:grid-cols-[20rem_minmax(0,1fr)]">
      <!--
        The master pane: the attempt's stage rail, always on screen. It owns
        its own loading, failure and empty states, so the inspector never has
        to speak for it.
      -->
      <aside aria-label="Run stages">
        <StageRail :state="run.attempts" />
      </aside>

      <Card>
        <CardContent>
          <Tabs class="gap-4" :model-value="run.tab" @update:model-value="run.setTab">
            <TabBar />
            <TaskRunPanels :id="rawId" />
          </Tabs>
        </CardContent>
      </Card>
    </div>
  </div>
</template>
