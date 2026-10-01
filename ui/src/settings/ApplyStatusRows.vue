<script setup lang="ts">
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import { useControlPlaneStore } from "@/stores/control-plane";
import { applyStateLabel, applyStateTone } from "./apply-status";

const props = defineProps<{ kind: string }>();
const store = useControlPlaneStore();
const rows = computed(() => store.applyStatusFor(props.kind));
const mode = computed(
  () => store.catalog.find((item) => item.kind === props.kind)?.apply_mode,
);
</script>

<template>
  <div v-if="rows.length" class="flex flex-col gap-2">
    <p class="text-xs font-medium text-muted-foreground">Applied by</p>
    <div v-for="row in rows" :key="row.process" class="flex flex-col gap-1">
      <div class="flex items-center gap-2 text-xs">
        <Badge :variant="applyStateTone(row.state, mode)">{{ applyStateLabel(row.state, mode) }}</Badge>
        <span class="font-mono">{{ row.process }}</span>
        <span v-if="row.state !== 'not-reporting'" class="text-muted-foreground"
          >version {{ row.version }}</span
        >
      </div>
      <p v-if="row.error" class="text-xs text-destructive">{{ row.error }}</p>
    </div>
  </div>
</template>
