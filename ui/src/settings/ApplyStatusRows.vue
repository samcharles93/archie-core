<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";

import { Badge } from "@/components/ui/badge";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useControlPlaneStore } from "@/stores/control-plane";
import { applyStateLabel, applyStateTone } from "./apply-status";

const now = ref(Date.now());
let timer: ReturnType<typeof setInterval>;
onMounted(() => { timer = setInterval(() => { now.value = Date.now(); }, 15_000); });
onUnmounted(() => clearInterval(timer));
function reportAge(at: string): string {
 const seconds = Math.max(0, Math.floor((now.value - Date.parse(at)) / 1000));
 return seconds < 60 ? `${seconds}s ago` : `${Math.floor(seconds / 60)}m ago`;
}
const props = defineProps<{ kind: string; compact?: boolean }>();
const store = useControlPlaneStore();
const rows = computed(() => store.applyStatusFor(props.kind));
const mode = computed(
  () => store.catalog.find((item) => item.kind === props.kind)?.apply_mode,
);

// The chip stands for the row that most needs attention; processes the
// resource does not apply to are not worth a chip.
const URGENCY = ["failed", "unknown", "pending-restart", "not-reporting", "running"];
const headline = computed(() =>
  rows.value
    .filter((row) => row.state !== "not-applicable")
    .sort((a, b) => URGENCY.indexOf(a.state) - URGENCY.indexOf(b.state))[0],
);
</script>

<template>
  <Popover v-if="compact">
    <PopoverTrigger v-if="headline" as-child>
      <button type="button" class="rounded-full" :aria-label="`Apply status: ${applyStateLabel(headline.state, mode)}`">
        <Badge :variant="applyStateTone(headline.state, mode)">{{ applyStateLabel(headline.state, mode) }}</Badge>
      </button>
    </PopoverTrigger>
    <PopoverContent align="end" class="w-80"><ApplyStatusRows :kind="kind" /></PopoverContent>
  </Popover>
  <div v-else-if="rows.length" class="flex flex-col gap-2">
    <p class="text-xs font-medium text-muted-foreground">{{ store.catalog.find(item => item.kind === kind)?.title ?? kind }} · apply status</p>
    <div v-for="row in rows" :key="row.process" class="flex flex-col gap-1">
      <div class="flex items-center gap-2 text-xs">
        <Badge :variant="applyStateTone(row.state, mode)">{{ applyStateLabel(row.state, mode) }}</Badge>
        <span class="font-mono">{{ row.process }}</span>
        <span v-if="row.state !== 'not-reporting' && row.state !== 'not-applicable'" class="text-muted-foreground"
          >applied v{{ row.version }} / stored v{{ row.storedVersion }}</span
        >
        <span v-if="row.reportedAt" class="text-muted-foreground">reported {{ reportAge(row.reportedAt) }}</span>
      </div>
      <p v-if="row.error" class="text-xs text-destructive">{{ row.error }}</p>
    </div>
  </div>
</template>
