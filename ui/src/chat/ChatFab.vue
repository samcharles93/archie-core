<script setup lang="ts">
import { MessageCircle, X } from "@lucide/vue";

import { cn } from "@/lib/utils";
import { CHAT_PANEL_ID } from "./state";

/**
 * The launcher: the one control that opens and closes the panel, which is why
 * there is no close button inside it.
 *
 * Both glyphs are stacked and cross-faded, so the button keeps one size and the
 * swap cannot reflow the dock. It stays visible while the panel is open -- and
 * on a phone, where the panel becomes a sheet above it, that visibility is the
 * only way out.
 */
defineProps<{ open: boolean }>();
const emit = defineEmits<{ toggle: [] }>();

// Stacked in one cell, so switching glyphs changes opacity and rotation rather
// than layout.
function glyph(visible: boolean): string {
  return cn(
    "col-start-1 row-start-1 grid place-items-center transition-[opacity,transform] duration-[140ms]",
    visible ? "opacity-100" : "-rotate-45 scale-[0.7] opacity-0",
  );
}
</script>

<template>
  <button
    type="button"
    class="relative z-10 grid size-14 cursor-pointer place-items-center rounded-full border border-border-strong bg-secondary text-primary shadow-[var(--shadow-md)] transition-transform duration-[140ms] hover:-translate-y-px focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary active:translate-y-0"
    :aria-label="open ? 'Close chat' : 'Chat with Archie'"
    :title="open ? 'Close chat' : 'Chat with Archie'"
    :aria-expanded="open"
    :aria-controls="CHAT_PANEL_ID"
    @click="emit('toggle')"
  >
    <span aria-hidden="true" :class="glyph(!open)">
      <MessageCircle :size="22" />
    </span>
    <span aria-hidden="true" :class="glyph(open)">
      <X :size="22" />
    </span>
  </button>
</template>
