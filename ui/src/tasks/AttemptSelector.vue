<script setup lang="ts">
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";

import { stageStatusMeta } from "./stage-rail";
import { useTaskRun } from "./use-task-run";

/**
 * The attempts of this task, each a pill carrying its own status and stage
 * count. Selection is the page's, and the stage rail reads the same response,
 * so the two views of the same task's attempts can never disagree about which
 * one is on screen.
 */
const run = useTaskRun();

const attempts = computed(() => run.attempts?.attempts ?? []);
const selected = computed(() => (run.attemptNumber == null ? "" : String(run.attemptNumber)));

function select(value: unknown): void {
  // Re-pressing the selected attempt deselects it (a single-typed group answers
  // ""), which is not an attempt: keep the selection rather than moving to
  // none.
  const n = Number(value);
  if (Number.isInteger(n) && n > 0) run.selectAttempt(n);
}

function stageCount(attempt: { stages?: unknown[] }): string {
  const n = (attempt.stages || []).length;
  return `${n} stage${n === 1 ? "" : "s"}`;
}
</script>

<template>
  <ToggleGroup
    v-if="attempts.length"
    type="single"
    variant="outline"
    :spacing="2"
    :model-value="selected"
    aria-label="Attempts of this task"
    class="mb-4 flex-wrap"
    @update:model-value="select"
  >
    <ToggleGroupItem
      v-for="attempt in attempts"
      :key="attempt.attempt"
      :value="String(attempt.attempt)"
      class="gap-2 rounded-full px-3"
    >
      <span class="font-medium">Attempt {{ attempt.attempt }}</span>
      <Badge :variant="stageStatusMeta(attempt.status).kind">{{ stageStatusMeta(attempt.status).label }}</Badge>
      <span class="text-xs text-fg-muted">{{ stageCount(attempt) }}</span>
    </ToggleGroupItem>
  </ToggleGroup>
</template>
