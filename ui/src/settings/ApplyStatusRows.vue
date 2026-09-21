<script setup lang="ts">
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import { useControlPlaneStore, type ApplyState } from "@/stores/control-plane";

const props = defineProps<{ kind: string }>();
const store = useControlPlaneStore();
const rows = computed(() => store.applyStatusFor(props.kind));

// A process that stopped reporting or never reported is never shown as an
// error: the setting may be fine and the process simply absent, which is a
// different thing an operator acts on differently.
const variants: Record<ApplyState, "ok" | "warn" | "danger" | "idle"> = {
  running: "ok",
  "pending-restart": "warn",
  failed: "danger",
  unknown: "warn",
  "not-reporting": "idle",
};

const labels: Record<ApplyState, string> = {
  running: "Running",
  "pending-restart": "Restart pending",
  failed: "Failed",
  unknown: "Not reporting",
  "not-reporting": "Never reported",
};
</script>

<template>
  <div v-if="rows.length" class="flex flex-col gap-2">
    <p class="text-xs font-medium text-muted-foreground">Applied by</p>
    <div v-for="row in rows" :key="row.process" class="flex flex-col gap-1">
      <div class="flex items-center gap-2 text-xs">
        <Badge :variant="variants[row.state]">{{ labels[row.state] }}</Badge>
        <span class="font-mono">{{ row.process }}</span>
        <span v-if="row.state !== 'not-reporting'" class="text-muted-foreground">version {{ row.version }}</span>
      </div>
      <p v-if="row.error" class="text-xs text-destructive">{{ row.error }}</p>
    </div>
  </div>
</template>
