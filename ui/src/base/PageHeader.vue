<script setup lang="ts">
/**
 * The shared page title and its actions. A component rather than per-page
 * markup because hand-built copies drift.
 */
defineProps<{ title: string }>();
</script>

<template>
  <div class="mb-6 flex flex-wrap items-start justify-between gap-5">
    <h1 class="text-2xl font-semibold tracking-[-0.02em]">{{ title }}</h1>
    <div class="flex flex-wrap items-center gap-2">
      <slot />
    </div>
  </div>
</template>
