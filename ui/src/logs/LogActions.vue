<script setup lang="ts">
import { Pause, Play, RefreshCw } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { loadLogs, paused } from "./state";

/**
 * Freeze the tail, or read the history again.
 *
 * Pause is a state as much as an action, so the button carries it: while the
 * list has stopped following the stream this is the only thing on the page
 * that says so, and the emphasis marks it.
 */
function togglePause(): void {
  paused.value = !paused.value;
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-2">
    <Button :variant="paused ? 'default' : 'outline'" @click="togglePause">
      <Play v-if="paused" data-icon="inline-start" />
      <Pause v-else data-icon="inline-start" />
      {{ paused ? "Resume" : "Pause" }}
    </Button>
    <Button variant="outline" @click="loadLogs">
      <RefreshCw data-icon="inline-start" />
      Refresh
    </Button>
  </div>
</template>
