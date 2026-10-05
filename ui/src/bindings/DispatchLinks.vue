<script setup lang="ts">
import { ref, watch } from "vue";

import { api } from "@/lib/api";
import { ago } from "@/lib/format";

/**
 * The dispatch ledger rows for one binding, capture or task, each linking the
 * other two ends: which binding evaluated which capture, and the task it
 * started or the reason it started none. `omit` drops the column the page
 * already is.
 */
export interface Dispatch {
  binding_id: string;
  binding_version: number;
  capture_id: string;
  task_id?: number;
  reason?: string;
  dispatched_at: string;
}

const props = defineProps<{
  kind: "bindings" | "captures" | "tasks";
  id: string;
}>();

const dispatches = ref<Dispatch[]>([]);
const failed = ref(false);

watch(
  () => [props.kind, props.id],
  async () => {
    failed.value = false;
    try {
      dispatches.value = await api.dispatches<Dispatch[]>(props.kind, props.id);
    } catch {
      dispatches.value = [];
      failed.value = true;
    }
  },
  { immediate: true },
);

const captureLink = (id: string) => ({
  path: "/events",
  query: { tab: "inspector", capture: id },
});
</script>

<template>
  <p v-if="failed" class="text-xs text-fg-muted">Dispatches unavailable.</p>
  <ul v-else-if="dispatches.length" class="flex flex-col gap-1 text-xs">
    <li
      v-for="d in dispatches"
      :key="d.binding_id + d.capture_id"
      class="flex flex-wrap items-center gap-x-2 font-mono text-fg-muted"
    >
      <RouterLink
        v-if="props.kind !== 'bindings'"
        :to="{ path: '/events', query: { tab: 'bindings' } }"
        class="hover:text-foreground"
        >binding {{ d.binding_id.slice(0, 8) }} v{{ d.binding_version }}</RouterLink
      >
      <RouterLink
        v-if="props.kind !== 'captures'"
        :to="captureLink(d.capture_id)"
        class="hover:text-foreground"
        >capture {{ d.capture_id.slice(0, 8) }}</RouterLink
      >
      <template v-if="props.kind !== 'tasks'">
        <RouterLink
          v-if="d.task_id"
          :to="`/tasks/${d.task_id}`"
          class="text-foreground hover:underline"
          >task #{{ d.task_id }}</RouterLink
        >
        <span v-else>{{ d.reason || "claimed, no task recorded" }}</span>
      </template>
      <span>{{ ago(d.dispatched_at) }}</span>
    </li>
  </ul>
  <p v-else-if="props.kind !== 'tasks'" class="text-xs text-fg-muted">
    No dispatches yet.
  </p>
</template>
