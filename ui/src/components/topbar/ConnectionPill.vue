<script setup lang="ts">
import { computed } from "vue";
import { storeToRefs } from "pinia";

import { StatusPill } from "@/components/ui/status-pill";
import { connectionStatus } from "@/lib/connection";
import { useLiveUpdatesStore } from "@/stores/live-updates";

const { streamState } = storeToRefs(useLiveUpdatesStore());
const status = computed(() => connectionStatus(streamState.value));
const pill = {
  ok: { tone: "neutral", dot: "live" },
  warn: { tone: "warn", dot: "warn" },
  danger: { tone: "danger", dot: "danger" },
} as const;
</script>

<template>
  <StatusPill
    :tone="pill[status.tone].tone"
    :dot="pill[status.tone].dot"
    role="status"
    class="max-sm:hidden"
    >{{ status.label }}</StatusPill
  >
</template>
