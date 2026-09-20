<script setup lang="ts">
import { storeToRefs } from "pinia";

import { Badge } from "@/components/ui/badge";
import { useLiveUpdatesStore } from "@/stores/live-updates";

const { streamKind, streamState } = storeToRefs(useLiveUpdatesStore());

/**
 * The state of the stream that is supposed to be adding to this list. Without
 * it a list that has stopped updating looks exactly like a quiet one.
 */
</script>

<template>
  <Badge :variant="streamKind">{{ streamState }}</Badge>
</template>
