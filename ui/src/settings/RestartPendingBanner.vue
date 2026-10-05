<script setup lang="ts">
import { computed } from "vue";

import { useControlPlaneStore } from "@/stores/control-plane";
import { restartPendingTitles } from "./apply-status";

const store = useControlPlaneStore();

// Read from what each process reports it is running, never from this page's
// own saves: a restart that already happened clears it. A restart-required
// kind earns this banner while some process still runs the version it had at
// boot; a live-apply kind reports its lag in its own applied-version rows
// rather than here.
const pending = computed(() =>
  restartPendingTitles(store.genericResources, (kind) => store.applyStatusFor(kind)),
);
</script>

<template>
  <div
    v-if="pending.length"
    role="status"
    class="mb-6 rounded-md border border-warn/40 bg-warn-soft px-4 py-3 text-sm"
  >
    <p class="font-medium text-warn">Restart pending</p>
    <p class="mt-0.5 text-fg-muted">
      {{ pending.join(", ") }}
    </p>
  </div>
</template>
