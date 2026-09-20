<script setup lang="ts">
import LogActions from "./LogActions.vue";

/** The page's title and what belongs beside it. */
</script>

<template>
  <div class="mb-5 flex flex-wrap items-start justify-between gap-5">
    <div>
      <h1 class="text-3xl font-semibold tracking-[-0.03em]">Logs</h1>
      <p class="mt-2 text-sm text-fg-muted">What Archie has been doing, newest last.</p>
    </div>
    <LogActions />
  </div>
</template>
