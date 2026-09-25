<script setup lang="ts">
import { computed } from "vue";

import { useControlPlaneStore } from "@/stores/control-plane";

const store = useControlPlaneStore();

// Read from what each process reports it is running, never from this page's
// own saves: a restart that already happened clears it.
const pending = computed(() =>
  store.genericResources
    .filter((item) => item.apply_mode === "restart-required")
    .filter((item) =>
      store.applyStatusFor(item.kind).some((row) => row.state === "pending-restart"),
    )
    .map((item) => item.title),
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
      {{ pending.join(", ") }} saved; running processes pick them up when archied restarts.
    </p>
  </div>
</template>
