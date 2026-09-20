<script setup lang="ts">
withDefaults(defineProps<{ size?: number }>(), { size: 22 });
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    aria-hidden="true"
    class="text-link shrink-0"
  >
    <path d="M12 2.5 21 20h-4.1L12 10.6 7.1 20H3l9-17.5Z" fill="currentColor" />
    <path d="M8.6 16.2h6.8" stroke="var(--background)" stroke-width="1.6" stroke-linecap="round" />
  </svg>
</template>
