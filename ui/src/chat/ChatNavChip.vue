<script setup lang="ts">
import { ArrowUpRight } from "@lucide/vue";

/**
 * A dashboard_navigate result: a clickable chip that routes the operator to a
 * dashboard page. It is an action, not evidence, so it is styled as a button
 * rather than a muted log line, and it visibly invites a click. It is the only
 * clickable navigation surface in the transcript.
 *
 * A RouterLink rather than a `#/path` anchor: history routing means the server
 * stores bare paths, and an anchor would reload the whole app to change one
 * segment.
 */
defineProps<{ path: string; label?: string }>();
</script>

<template>
  <RouterLink
    :to="path"
    :title="`Go to ${label || path}`"
    class="inline-flex max-w-full items-center gap-1.5 rounded-full border border-primary/45 bg-primary-soft px-2.5 py-1 text-xs font-semibold text-primary transition-colors hover:border-primary hover:bg-primary/15 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
  >
    <ArrowUpRight aria-hidden="true" class="size-3.5 shrink-0" />
    <span class="truncate">{{ label || path }}</span>
  </RouterLink>
</template>
