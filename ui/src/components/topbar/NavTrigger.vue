<script setup lang="ts">
defineProps<{ to?: string; current?: boolean }>();
</script>

<template>
  <component
    :is="to ? 'RouterLink' : 'button'"
    :to="to"
    :type="to ? undefined : 'button'"
    :aria-current="to && current ? 'page' : undefined"
    :data-current="current ? '' : undefined"
    class="text-muted-foreground hover:text-foreground hover:bg-accent data-current:text-foreground data-current:bg-accent focus-visible:ring-ring/50 inline-flex h-8 shrink-0 items-center gap-1 rounded-md px-2.5 text-sm font-medium whitespace-nowrap transition-colors outline-none focus-visible:ring-[3px]"
  >
    <slot />
  </component>
</template>
