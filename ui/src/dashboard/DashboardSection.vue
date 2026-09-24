<script setup lang="ts">
/**
 * One labelled region of the dashboard.
 *
 * Panels are grouped under a quiet section label so the page reads as one
 * surface with regions rather than a scatter of unrelated cards.
 */
defineProps<{ title: string; note?: string }>();
</script>

<template>
  <section class="mb-5">
    <div class="mb-3 flex items-baseline gap-3 pl-1">
      <h2
        class="text-xs font-semibold tracking-[0.08em] text-fg-subtle uppercase"
      >
        {{ title }}
      </h2>
      <span v-if="note" class="text-xs text-fg-subtle">{{ note }}</span>
    </div>
    <slot />
  </section>
</template>
