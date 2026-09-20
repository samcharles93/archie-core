<script setup lang="ts">
/**
 * A panel still in flight. One line, said in the panel's own words, because a
 * panel that renders blank while it waits reads as a panel with nothing in it.
 */
defineProps<{ label: string }>();
</script>

<template>
  <p class="py-5 text-sm text-fg-muted">{{ label }}</p>
</template>
