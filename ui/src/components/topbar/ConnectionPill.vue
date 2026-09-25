<script setup lang="ts">
import { computed } from "vue";
import { storeToRefs } from "pinia";

import { connectionStatus } from "@/lib/connection";
import { useLiveUpdatesStore } from "@/stores/live-updates";

const { streamState } = storeToRefs(useLiveUpdatesStore());
const status = computed(() => connectionStatus(streamState.value));
const look = {
  ok: { dot: "bg-primary", text: "text-fg-subtle" },
  warn: { dot: "bg-warn", text: "text-warn" },
  danger: { dot: "bg-danger", text: "text-danger" },
} as const;
</script>

<template>
  <span role="status" class="inline-flex items-center gap-1.5 px-1 text-xs max-sm:hidden" :class="look[status.tone].text">
    <span class="size-1.5 rounded-full" :class="look[status.tone].dot" aria-hidden="true" />
    {{ status.label }}
  </span>
</template>
