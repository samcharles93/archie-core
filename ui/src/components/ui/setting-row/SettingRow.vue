<script setup lang="ts">
import { cn } from "@/lib/utils";

defineProps<{
  label: string;
  hint?: string;
  /** id of the control, so the label targets it. */
  for?: string;
  /** Risky settings sit in a bordered danger row. */
  tone?: "default" | "danger";
  class?: string;
}>();
</script>

<template>
  <div
    :class="
      cn(
        'grid gap-2 border-t border-border py-4 md:grid-cols-[17rem_1fr] md:gap-8',
        tone === 'danger' && 'rounded-md border border-danger/40 bg-danger-soft px-4',
        $props.class,
      )
    "
  >
    <div class="min-w-0">
      <label :for="$props.for" class="text-[13px] font-medium text-foreground">{{ label }}</label>
      <p v-if="hint" class="mt-0.5 text-xs leading-normal text-fg-subtle">{{ hint }}</p>
    </div>
    <div class="min-w-0">
      <slot />
    </div>
  </div>
</template>
