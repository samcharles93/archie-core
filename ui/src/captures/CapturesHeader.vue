<script setup lang="ts">
import CaptureStatus from "./CaptureStatus.vue";

/** The page title and stream status. */
</script>

<template>
  <div class="mb-5 flex flex-wrap items-start justify-between gap-5">
    <h1 class="text-3xl font-semibold tracking-[-0.03em]">Event inspector</h1>
    <div class="flex flex-wrap items-center gap-2">
      <CaptureStatus />
    </div>
  </div>
</template>
