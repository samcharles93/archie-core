<script setup lang="ts">
/**
 * The head every page opens with: what the page is, in a line, and the actions
 * that belong to it. A component rather than per-page markup because every page
 * has one, and hand-built copies drift.
 */
defineProps<{ title: string; subtitle?: string }>();
</script>

<template>
  <div class="mb-6 flex flex-wrap items-start justify-between gap-5">
    <div>
      <h1 class="text-2xl font-semibold tracking-[-0.02em]">{{ title }}</h1>
      <p v-if="subtitle" class="mt-1 text-sm text-fg-muted">{{ subtitle }}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <slot />
    </div>
  </div>
</template>
