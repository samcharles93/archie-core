<script setup lang="ts">
import { computed } from "vue";

import LogRow from "@/base/LogRow.vue";
import { Button } from "@/components/ui/button";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { api } from "@/lib/api";

import PanelLoading from "./PanelLoading.vue";
import TaskLogFilters from "./TaskLogFilters.vue";
import { EMPTY_LOG_FILTERS, type LogFilters, type LogState } from "./task-run";

/**
 * A task attempt's log pane.
 *
 * Three read outcomes are kept apart, because only one of them is about the
 * operator's configuration or about this attempt:
 *
 *   disabled       -- this process cannot read task logs at all (deployment)
 *   found: false   -- a reader exists and this attempt has no log file
 *   entries: []    -- a log exists and nothing matches the current filter
 *
 * The filter controls render ABOVE all three, and above the loading and failed
 * states too. Putting them inside the success branch is how a deployment that
 * cannot read logs ends up showing an empty filtered pane instead of saying so.
 * They render when the page can act on a filter change (`filterable`); a
 * control that filters nothing is worse than no control. The ordering rule
 * belongs here either way.
 */
const props = withDefaults(
  defineProps<{
    state: LogState | null | undefined;
    /** The attempt this pane is showing, 0 meaning the current one. */
    attempt: number;
    stages?: string[];
    filters?: LogFilters;
    /** False when the host holds no filter state (the list page's inline pane). */
    filterable?: boolean;
    taskId?: string | number | null;
  }>(),
  {
    stages: () => [],
    filters: () => ({ ...EMPTY_LOG_FILTERS }),
    filterable: true,
    taskId: null,
  },
);

defineEmits<{ filter: [next: LogFilters] }>();

const resolvedAttempt = computed(() => Number(props.attempt) || Number(props.state?.attempt) || 0);

/** The pane's state, decided once so the announcement and the body can never
 * disagree about which case is on screen. */
const view = computed(() => {
  const state = props.state;
  if (state === undefined) return { kind: "loading" as const, status: "Loading this attempt's log" };
  if (state === null) return { kind: "failed" as const, status: "This attempt's log could not be loaded" };
  // A reader exists in this process but the attempt has no log file. That is a
  // fact about the attempt, and saying "logging was not enabled" here (which
  // this panel used to do) told the operator to go and change a setting that
  // was already correct.
  if (state.found === false && !state.disabled) {
    return { kind: "nolog" as const, status: "No log recorded for this attempt" };
  }
  // This process cannot read task logs at all. That IS about the deployment,
  // and it is the only case where the pane may say so.
  if (state.disabled) return { kind: "disabled" as const, status: "This dashboard cannot read task logs" };
  const entries = state.entries || [];
  if (!entries.length) {
    return {
      kind: "empty" as const,
      status: "This attempt's log has no entries matching the current filter",
      entries,
    };
  }
  return {
    kind: "entries" as const,
    status: `${entries.length} log entr${entries.length === 1 ? "y" : "ies"} shown`,
    entries,
  };
});

const download = computed(() =>
  props.taskId != null ? api.taskLogDownloadURL(String(props.taskId), resolvedAttempt.value || null) : null,
);
</script>

<template>
  <div>
    <TaskLogFilters
      v-if="filterable"
      class="mb-3"
      :filters="filters"
      :stages="stages"
      :attempt="resolvedAttempt"
      @filter="$emit('filter', $event)"
    />

    <!-- The pane's state, announced once. It is a live region for STATE, not
         for the log's lines: putting aria-live on the list itself would read
         every entry aloud on every load, which for a 500-entry pane is
         unusable. -->
    <p class="sr-only" role="status" aria-live="polite" aria-atomic="true">{{ view.status }}</p>

    <PanelLoading v-if="view.kind === 'loading'" label="Loading attempt log…" />

    <div v-else-if="view.kind === 'failed'" class="flex flex-col items-start gap-2 py-3">
      <p class="text-sm text-fg-muted">
        Could not load this attempt's log. Live updates will try again when the daemon reconnects.
      </p>
    </div>

    <Empty v-else-if="view.kind === 'nolog'">
      <EmptyHeader>
        <EmptyTitle>No log recorded for this attempt</EmptyTitle>
        <EmptyDescription>This attempt produced no output, or it has not started writing yet.</EmptyDescription>
      </EmptyHeader>
    </Empty>

    <Empty v-else-if="view.kind === 'disabled'">
      <EmptyHeader>
        <EmptyTitle>No persisted log for this attempt</EmptyTitle>
        <EmptyDescription>The log service is not reporting a reader, so the file cannot be read.</EmptyDescription>
      </EmptyHeader>
    </Empty>

    <Empty v-else-if="view.kind === 'empty'">
      <EmptyHeader>
        <EmptyTitle>Nothing recorded</EmptyTitle>
      </EmptyHeader>
      <EmptyContent v-if="download">
        <Button variant="outline" size="sm" as-child>
          <a :href="download" download>Download log</a>
        </Button>
      </EmptyContent>
    </Empty>

    <template v-else>
      <!-- The detail page's log is the primary content, so it takes a framed
           read surface of its own and the tallest one on the page. -->
      <div class="max-h-[62vh] overflow-y-auto rounded-sm border border-border bg-card px-2">
        <LogRow v-for="(entry, i) in view.entries" :key="i" :entry="entry" />
      </div>
      <Button v-if="download" variant="outline" size="sm" class="mt-3" as-child>
        <a :href="download" download>Download log</a>
      </Button>
    </template>
  </div>
</template>
