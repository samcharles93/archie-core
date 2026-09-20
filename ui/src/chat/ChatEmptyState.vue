<script setup lang="ts">
/**
 * What the panel opens on before there is a conversation: an invitation, and
 * three prompts that fill the composer. The text is handed up rather than
 * typed into the composer here, because the composer owns its own focus.
 */
const emit = defineEmits<{ prompt: [text: string] }>();

// Prompts an operator might actually ask, rather than demonstrations of the
// feature.
const PROMPTS = ["Summarise active tasks", "Show running agents", "Check system status"];
</script>

<template>
  <div class="w-full self-center justify-self-center py-6 text-center">
    <span
      aria-hidden="true"
      class="mx-auto mb-3 grid size-9 place-items-center rounded-xl border border-border-strong font-bold text-primary"
      >A</span
    >
    <h2 class="text-lg font-semibold tracking-[-0.02em]">What are we working on?</h2>
    <p class="mt-2 mb-4 text-sm text-fg-muted">Ask Archie anything, or start with one of these prompts.</p>
    <div class="grid gap-2">
      <button
        v-for="prompt in PROMPTS"
        :key="prompt"
        type="button"
        class="rounded-full border border-border bg-muted px-3 py-2 text-left text-sm text-fg-muted transition-colors hover:border-border-strong hover:text-foreground"
        @click="emit('prompt', prompt)"
      >
        {{ prompt }}
      </button>
    </div>
  </div>
</template>
