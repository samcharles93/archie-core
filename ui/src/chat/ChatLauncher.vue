<script setup lang="ts">
import { onUnmounted, ref, watch } from "vue";

import ChatFab from "./ChatFab.vue";
import ChatPanel from "./ChatPanel.vue";
import { escapeOwnedByLayer } from "./state";

/**
 * Chat is a floating launcher and the panel it opens, not a route: the operator
 * asks Archie about the page they are already on, so navigating must not close
 * it and opening it must not replace the work that prompted the question.
 *
 * Deliberately not a modal: no scrim, no body scroll lock, no focus trap. The
 * page stays readable, scrollable and clickable while the panel is open, which
 * is what "ask about this page" requires.
 *
 * It hosts a single panel instance, so the session and its stream survive
 * navigation rather than being rebuilt per page.
 *
 * This element is mounted outside the app shell: `.shell`'s backdrop-filter
 * makes it a containing block for `position: fixed`, so a launcher nested
 * inside it would anchor to the shell and ride off the bottom of a long page.
 */
const open = ref(false);
const fab = ref<{ $el: HTMLButtonElement } | null>(null);

function close(): void {
  open.value = false;
  // Focus goes back to the control that opened the panel; otherwise a keyboard
  // user who dismisses it lands at the top of the document.
  fab.value?.$el?.focus();
}

function toggle(): void {
  if (open.value) close();
  else open.value = true;
}

function onKeydown(event: KeyboardEvent): void {
  // A focused control (the composer dismissing its palette, or the topbar
  // search) already handled Escape via preventDefault; closing the panel too
  // would make that menu impossible to dismiss on its own.
  if (event.defaultPrevented || event.key !== "Escape") return;
  // The same rule for a layer that owns Escape by being open rather than by
  // holding focus: a popover portalled out of the panel listens on document
  // too, and the panel registered its own handler first, so the flag is what
  // keeps the innermost layer's dismissal from taking the panel with it.
  if (escapeOwnedByLayer()) return;
  close();
}

watch(open, (isOpen) => {
  if (isOpen) document.addEventListener("keydown", onKeydown);
  else document.removeEventListener("keydown", onKeydown);
});

onUnmounted(() => document.removeEventListener("keydown", onKeydown));
</script>

<template>
  <div
    class="fixed right-6 bottom-6 z-40 max-md:right-4 max-md:bottom-4"
  >
    <ChatPanel :open="open" />
    <ChatFab ref="fab" :open="open" @toggle="toggle" />
  </div>
</template>
