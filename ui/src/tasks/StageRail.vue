<script setup lang="ts">
import { computed } from "vue";

import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Badge } from "@/components/ui/badge";

import PanelError from "./PanelError.vue";
import PanelLoading from "./PanelLoading.vue";
import StageRow from "./StageRow.vue";
import { stageStatusMeta } from "./stage-rail";
import { useTaskRun } from "./use-task-run";
import type { AttemptsState, Stage } from "./task-run";

/**
 * The per-attempt stage rail.
 *
 * The rail is built from `GET /api/tasks/{id}/attempts`, which returns each
 * attempt with its stages inline, so the selector and the rail can never
 * disagree about which attempt is on screen. The rail only ever renders one
 * attempt: merging two attempts by stage name is the failure this design
 * exists to avoid.
 *
 * A per-run elapsed time is deliberately NOT rendered. `waiting_human` can span
 * days, so wall time from an attempt's start is not a duration of work; only
 * per-stage durations exist and only those are shown.
 */
const props = defineProps<{ state: AttemptsState | null | undefined }>();

const run = useTaskRun();

const attempts = computed(() => props.state?.attempts || []);

const attempt = computed(() => attempts.value.find((a) => Number(a.attempt) === Number(run.attemptNumber)));

const meta = computed(() => stageStatusMeta(attempt.value?.status));
const stages = computed(() => attempt.value?.stages || []);

/**
 * Master-detail selection (docs/prds/task-run-master-detail.md): clicking a
 * stage commands the inspector to the log tab with this stage's filter set.
 * Clicking the selected stage clears the filter back to all stages. The
 * selection is a command, not a binding: the operator can change the filter
 * afterwards and the rail does not fight them. A stage with no name has
 * nothing to filter on and is not selectable.
 */
function selectedStage(stage: Stage): boolean {
  return run.tab === "log" && Boolean(stage.name) && run.filters.stage === stage.name;
}

function selectStage(stage: Stage): void {
  if (!stage.name) return;
  run.setFilters({ stage: selectedStage(stage) ? "" : stage.name, level: run.filters.level });
  run.setTab("log");
}

/** An attempt's start is rendered as an absolute instant, never as "started 2
 * days ago": on a still-running attempt a relative stamp reads as two days of
 * work, which is exactly the elapsed-time claim the design forbids. */
const startedAt = computed(() => {
  const value = attempt.value?.started_at;
  if (!value) return "start time not recorded";
  const when = new Date(value);
  if (Number.isNaN(when.getTime())) return "start time not recorded";
  return `started ${when.toISOString().replace("T", " ").slice(0, 16)} UTC`;
});
</script>

<template>
  <PanelLoading v-if="state === undefined" label="Loading this task's attempts…" />
  <PanelError
    v-else-if="state === null"
    title="Could not load this task's attempts"
    detail="archied did not answer for this task's run history. Live updates will try again when the daemon reconnects."
  />
  <template v-else>
    <Empty v-if="!attempts.length">
      <EmptyHeader>
        <EmptyTitle>No attempts recorded</EmptyTitle>
      </EmptyHeader>
    </Empty>

    <Empty v-else-if="!attempt">
      <EmptyHeader>
        <EmptyTitle>Attempt {{ run.attemptNumber }} is not recorded</EmptyTitle>
        <EmptyDescription>This task recorded attempt {{ attempts.map((a) => a.attempt).join(", ") }}.</EmptyDescription>
      </EmptyHeader>
    </Empty>

    <div v-else>
      <div class="mb-4 flex flex-wrap items-center gap-3">
        <span class="font-semibold">Attempt {{ attempt.attempt }}</span>
        <Badge :variant="meta.kind">{{ meta.label }}</Badge>
        <span class="text-xs text-fg-muted">{{ startedAt }}</span>
      </div>
      <ol v-if="stages.length" :aria-label="`Stages recorded in attempt ${attempt.attempt}`">
        <StageRow
          v-for="(stage, i) in stages"
          :key="`${stage.seq ?? i}:${stage.name}`"
          :stage="stage"
          :attempt-number="attempt.attempt"
          :events="run.events"
          :selected="selectedStage(stage)"
          @select="selectStage(stage)"
        />
      </ol>
      <Empty v-else>
        <EmptyHeader>
          <EmptyTitle>No stages recorded for this attempt</EmptyTitle>
        </EmptyHeader>
      </Empty>
      <p class="mt-3 max-w-[70ch] text-xs text-fg-muted">
        <strong>ok</strong> means the stage returned without error. Archie records no exit code and does not verify
        that the work was correct, so this is a progress status, not a check result.
      </p>
    </div>

    <!-- Rendered in every loaded state, the empty ones included: a deployment
         where every event predates the attempt column answers with no attempts
         at all, and reporting "No attempts recorded" there without the count
         would present unattributable history as nothing having happened. -->
    <p v-if="Number(state.unattributed_events) > 0" class="mt-3 max-w-[70ch] text-xs text-fg-muted">
      {{ Number(state.unattributed_events) }} event{{ Number(state.unattributed_events) === 1 ? "" : "s" }} on this
      task predate attempt attribution and cannot be assigned to a run.
    </p>
  </template>
</template>
