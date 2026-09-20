<script setup lang="ts">
import CaptureActions from "./CaptureActions.vue";
import CaptureStatus from "./CaptureStatus.vue";

/** The page's title, and what belongs beside it: where the stream has got to,
 * and a way to ask for the list again. */
</script>

<template>
  <div class="mb-5 flex flex-wrap items-start justify-between gap-5">
    <div>
      <h1 class="text-3xl font-semibold tracking-[-0.03em]">Event inspector</h1>
      <p class="mt-2 text-sm text-fg-muted">Real inbound payloads archie has captured, most recent first.</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <CaptureStatus />
      <CaptureActions />
    </div>
  </div>
</template>
