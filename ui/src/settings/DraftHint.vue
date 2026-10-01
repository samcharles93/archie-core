<script setup lang="ts">
import { computed } from "vue";

import { useControlPlaneStore } from "@/stores/control-plane";
import { formatValue } from "./changes";

/**
 * The previous value of an edited field, inline at the control: "was 1m0s".
 *
 * It reads the same per-kind diff the save bar and the route guard read, so
 * the hint, the review drawer and the dirty count can never disagree about
 * whether a field changed.
 */
const props = defineProps<{ kind: string; path: string }>();
const store = useControlPlaneStore();
const change = computed(() => store.changeFor(props.kind, props.path));
</script>

<template>
  <span v-if="change" class="text-xs whitespace-nowrap text-fg-subtle">
    was <s class="font-mono">{{ formatValue(change.before) }}</s>
  </span>
</template>
