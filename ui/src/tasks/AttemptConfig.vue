<script setup lang="ts">
import { computed } from "vue";

import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { configSchema } from "@/lib/task-meta";

import JsonBlock from "./JsonBlock.vue";
import PanelError from "./PanelError.vue";
import PanelLoading from "./PanelLoading.vue";
import { selectConfigEvent } from "./attempt-config";
import type { TaskEvent } from "./task-run";

/**
 * The effective configuration one attempt ran under, taken from the task's own
 * event stream. See attempt-config.ts for why this panel issues no request and
 * why the selection is by kind AND attempt.
 */
const props = defineProps<{
  events: TaskEvent[] | null | undefined;
  attempt: number | null;
}>();

const event = computed(() => selectConfigEvent(props.events, props.attempt));

// An unrecognised schema is shown verbatim with a note rather than
// reinterpreted, so a server-side bump degrades to the raw view.
const schema = computed(() => {
  const data = event.value?.data || {};
  return typeof data.schema === "string" ? data.schema : "";
});
const document = computed(() => event.value?.data?.document ?? {});
const recognised = computed(() => schema.value === configSchema());
</script>

<template>
  <PanelLoading v-if="events === undefined" label="Loading this task's events…" />
  <PanelError
    v-else-if="events === null"
    title="Could not load this task's events"
    detail="The run configuration could not be read. Live updates will try again when the daemon reconnects."
  />

  <Empty v-else-if="!event">
    <EmptyHeader>
      <EmptyTitle>No configuration captured</EmptyTitle>
    </EmptyHeader>
  </Empty>

  <div v-else>
    <div class="mb-3 text-xs text-fg-muted">
      Captured {{ event.at || "at an unrecorded time"
      }}<span v-if="event.stage"> in stage {{ event.stage }}</span>
    </div>
    <JsonBlock v-if="recognised" :value="document" />
    <template v-else>
      <p class="mb-2 text-sm text-warn">Unknown schema{{ schema ? ` (${schema})` : "" }} — shown verbatim.</p>
      <JsonBlock :value="event.data" />
    </template>
  </div>
</template>
