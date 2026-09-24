<script setup lang="ts">
import { Badge } from "@/components/ui/badge";
import { meta, streamKind, streamState } from "./state";

/**
 * What the list is showing, and the state of the stream that is supposed to be
 * adding to it. The two belong together: "Live only" says nothing on its own
 * when the stream behind those lines has stopped.
 */
</script>

<template>
  <div class="flex flex-wrap items-center gap-3">
    <span class="min-w-0 break-words font-mono text-xs text-fg-subtle">{{
      meta
    }}</span>
    <Badge :variant="streamKind">{{ streamState }}</Badge>
  </div>
</template>
