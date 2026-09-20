<script setup lang="ts">
import { computed } from "vue";

import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";

import JsonBlock from "./JsonBlock.vue";
import PanelError from "./PanelError.vue";
import PanelLoading from "./PanelLoading.vue";
import { CONFIG_SCHEMA, selectConfigEvent } from "./attempt-config";
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

defineEmits<{ retry: [] }>();

const event = computed(() => selectConfigEvent(props.events, props.attempt));

// An unrecognised schema is shown verbatim with a note rather than
// reinterpreted, so a server-side bump degrades to the raw view.
const schema = computed(() => {
  const data = event.value?.data || {};
  return typeof data.schema === "string" ? data.schema : "";
});
const document = computed(() => event.value?.data?.document ?? {});
const recognised = computed(() => schema.value === CONFIG_SCHEMA);
</script>

<template>
  <PanelLoading v-if="events === undefined" label="Loading this task's events…" />
  <PanelError
    v-else-if="events === null"
    title="Could not load this task's events"
    detail="The run configuration could not be read — retry, or check the daemon."
    @retry="$emit('retry')"
  />

  <Empty v-else-if="!event">
    <EmptyHeader>
      <EmptyTitle>Not captured for this run</EmptyTitle>
      <EmptyDescription>
        Archie records the effective configuration when it dispatches an attempt. Attempt {{ attempt }} has no such
        record — it predates the record, or the capture did not run. Nothing here says the run used the defaults.
      </EmptyDescription>
    </EmptyHeader>
  </Empty>

  <div v-else>
    <p class="mb-2 max-w-[75ch] text-sm text-fg-muted">
      This is attempt {{ event.attempt }}'s effective task-runtime configuration: a non-secret subset covering bot
      identity, models, limits, budgets, dispatch, diff cap, notifications, forge host and tool policy. It is not the
      dashboard configuration view — providers, repositories, identities, credentials and lock state are not part of
      it.
    </p>
    <div class="mb-3 text-xs text-fg-muted">
      Captured {{ event.at || "at an unrecorded time"
      }}<span v-if="event.stage"> in stage {{ event.stage }}</span>
    </div>
    <JsonBlock v-if="recognised" :value="document" />
    <template v-else>
      <p class="mb-2 text-sm text-warn">
        Recorded in an unknown schema{{ schema ? ` (${schema})` : "" }}. The raw payload is shown verbatim; nothing
        here reinterprets it.
      </p>
      <JsonBlock :value="event.data" />
    </template>
  </div>
</template>
