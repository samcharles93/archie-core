<script setup lang="ts">
import JsonBlock from "./JsonBlock.vue";
import PanelError from "./PanelError.vue";
import PanelLoading from "./PanelLoading.vue";
import type { DebugState } from "./task-run";

/**
 * Raw debug view: the stored task record and the task's events, verbatim.
 *
 * The events are deliberately NOT filtered to the selected attempt. A debug
 * view that silently hid events would be worse than useless, so every event is
 * shown and each carries its own `attempt` for the operator to attribute. The
 * selected attempt is reported in the note and in the envelope's own `attempt`
 * field; nothing here is summarised, projected or prettified beyond JSON
 * indentation.
 */
defineProps<{
  state: DebugState | null | undefined;
  attempt: number | null;
}>();

defineEmits<{ retry: [] }>();
</script>

<template>
  <PanelLoading v-if="state === undefined" label="Loading the stored record…" />
  <PanelError
    v-else-if="state === null"
    title="Could not load the stored record"
    detail="archied did not answer for this task's debug view. It may be restarting — retry, or check the daemon."
    @retry="$emit('retry')"
  />
  <div v-else>
    <p class="mb-3 text-sm text-fg-muted">The stored record and every event, verbatim.</p>
    <JsonBlock :value="state" />
  </div>
</template>
