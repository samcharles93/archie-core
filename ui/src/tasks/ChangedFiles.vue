<script setup lang="ts">
import { computed } from "vue";

import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";

import ChangeCapture from "./ChangeCapture.vue";
import PanelError from "./PanelError.vue";
import PanelLoading from "./PanelLoading.vue";
import type { ChangesState, TaskRecord } from "./task-run";

/**
 * What one attempt changed, as archied captured it.
 *
 * A capture with `found: true` and no readable payload is NOT reported as "no
 * capture was recorded": the server reports `found` from the event's existence
 * precisely so the two stay apart. One is a read failure, the other is a
 * statement about the run -- and a statement this panel is not entitled to make
 * on a read failure.
 */
const props = defineProps<{
  state: ChangesState | null | undefined;
  task: TaskRecord | null;
}>();

defineEmits<{ retry: [] }>();

const captures = computed(() => props.state?.captures || []);

// A capture event WAS recorded but its payload could not be read.
const undecodable = computed(() => props.state?.found === true && !captures.value.length);
const nothingRecorded = computed(() => !props.state?.found || !captures.value.length);
</script>

<template>
  <PanelLoading v-if="state === undefined" label="Loading changed files…" />
  <PanelError
    v-else-if="state === null"
    title="Could not load this attempt's changed files"
    detail="archied did not answer for this attempt. It may be restarting — retry, or check the daemon."
    @retry="$emit('retry')"
  />

  <Empty v-else-if="undecodable">
    <EmptyHeader>
      <EmptyTitle>A change capture was recorded but could not be read</EmptyTitle>
      <EmptyDescription>
        This attempt has a change capture event, but this dashboard could not decode its payload. That is a read
        failure, not a statement about what the attempt changed.
      </EmptyDescription>
    </EmptyHeader>
  </Empty>

  <Empty v-else-if="nothingRecorded">
    <EmptyHeader>
      <EmptyTitle>No change capture was recorded for this attempt</EmptyTitle>
      <EmptyDescription>
        Archie records what an attempt changed when it commits or pushes that work. Nothing was recorded here — the
        attempt may predate capture, or it produced no commit. This is not the same as "no files changed".
      </EmptyDescription>
    </EmptyHeader>
  </Empty>

  <div v-else class="flex flex-col">
    <ChangeCapture
      v-for="(capture, i) in captures"
      :key="`${capture.captured_at || ''}:${i}`"
      :capture="capture"
      :task="task"
      :class="i > 0 ? 'mt-5 border-t border-border pt-4' : ''"
    />
  </div>
</template>
