<script setup lang="ts">
/**
 * The key/value grid the configuration rows sit in.
 *
 * The list owns the three column tracks and every row adopts them with
 * subgrid, so all values start at the same x (the widest label's edge) and
 * every action column lines up. A row that declared its own tracks cannot do
 * that: a `max-content` label track resolves per row and the values come out
 * ragged (the fault this layout was rewritten for).
 *
 * An engine without subgrid gets a single-column list, so each row is still a
 * full-width item and the row's own bounded tracks do the alignment instead.
 */
</script>

<template>
  <div
    class="grid grid-cols-1 md:supports-[grid-template-columns:subgrid]:grid-cols-[max-content_minmax(0,1fr)_auto]"
  >
    <slot />
  </div>
</template>
