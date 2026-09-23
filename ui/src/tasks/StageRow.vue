<script setup lang="ts">
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

import { agentReports, stageStatusMeta } from "./stage-rail";
import { duration } from "./timeline-event";
import type { Stage, TaskEvent } from "./task-run";

/**
 * One stage of one attempt: its name, its status word, its own duration, the
 * error it returned if it failed, and what the agent said about itself.
 *
 * No checkmark and no exit status: see stage-rail.ts for what a stage status
 * does and does not mean.
 */
const props = defineProps<{
  stage: Stage;
  attemptNumber: number;
  events: TaskEvent[] | null | undefined;
  selected?: boolean;
}>();

const emit = defineEmits<{ select: [] }>();

const meta = computed(() => stageStatusMeta(props.stage.status));
const ran = computed(() => duration(props.stage.duration_ms));
const reports = computed(() => agentReports(props.events, props.attemptNumber, props.stage.name));

// The node carries the status as a shape, the same way the label's own kind
// does; the connector behind it is what makes the stages read as one run rather
// than as unrelated rows.
const nodeClass = computed(() =>
  cn(
    "relative mt-1 ml-1 size-3.5 shrink-0 rounded-full border-2 bg-card",
    props.stage.status === "ok"
      ? "border-ok"
      : props.stage.status === "failed"
        ? "border-danger"
        : props.stage.status === "interrupted"
          ? "border-warn"
          : props.stage.status === "running"
            ? "border-info"
            : "border-border-strong",
  ),
);
</script>

<template>
  <li
    class="relative grid grid-cols-[1.75rem_1fr] gap-2 pb-4 before:absolute before:top-4 before:bottom-0 before:left-[0.6875rem] before:w-px before:bg-border-strong last:before:hidden max-[700px]:grid-cols-1 max-[700px]:border-b max-[700px]:border-border max-[700px]:pb-3 max-[700px]:before:hidden"
    :class="props.selected ? 'rounded-md bg-primary-soft' : ''"
  >
    <span :class="nodeClass" aria-hidden="true" class="max-[700px]:hidden" />
    <div class="min-w-0" :class="props.selected ? '-my-2 rounded-md py-2' : ''">
      <!--
        Selecting a stage commands the inspector to this stage's log (the
        master-detail contract). The control is a real button so keyboard
        activation works without the row carrying a second role.
      -->
      <button
        type="button"
        class="flex w-full flex-wrap items-center gap-2 rounded text-left"
        :aria-pressed="props.selected ?? false"
        @click="emit('select')"
      >
        <!-- A stage name is one unbroken token often enough that it has to be
             allowed to break, or a long one pushes the status and duration off
             the row. -->
        <span class="min-w-0 font-mono font-medium break-words hover:underline" :class="props.selected ? 'text-primary' : ''">{{ stage.name || "(unnamed stage)" }}</span>
        <Badge :variant="meta.kind">{{ meta.label }}</Badge>
        <span class="text-xs text-fg-muted">{{ ran ? `ran for ${ran}` : "duration not recorded" }}</span>
      </button>
      <div
        v-if="stage.error"
        class="mt-2 rounded-sm border-l-[3px] border-danger bg-danger-soft px-3 py-2 text-sm break-words whitespace-pre-wrap text-fg-muted"
      >
        {{ stage.error }}
      </div>
      <div v-for="(report, i) in reports" :key="i" class="mt-2 text-xs text-fg-muted">
        <span>Agent's own report, not verified by archie: </span>
        {{ [report.title, report.detail].filter(Boolean).join(" · ") }}
      </div>
    </div>
  </li>
</template>
