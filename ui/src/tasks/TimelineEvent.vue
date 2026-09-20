<script setup lang="ts">
import { computed } from "vue";

import { ago } from "@/lib/format";

import { describeTimelineEvent } from "./timeline-event";
import type { TaskEvent } from "./task-run";

/**
 * One entry in a task's event timeline: what happened, any detail the event
 * carries, and when.
 *
 * It renders an `<li>`, so its host supplies the list. The words come from
 * describeTimelineEvent, which the stage rail's agent reports read too, so a
 * timeline and a rail can never describe the same event differently.
 */
const props = defineProps<{ event: TaskEvent }>();

const line = computed(() => describeTimelineEvent(props.event));
</script>

<template>
  <li class="flex items-start gap-3">
    <span class="mt-[5px] size-2 shrink-0 rounded-full bg-primary" aria-hidden="true" />
    <div>
      <div class="text-sm font-medium">{{ line.title }}</div>
      <div v-if="line.detail" class="mt-0.5 text-xs text-fg-muted">{{ line.detail }}</div>
      <div class="mt-0.5 text-xs text-fg-subtle">{{ ago(event.at) }}</div>
    </div>
  </li>
</template>
