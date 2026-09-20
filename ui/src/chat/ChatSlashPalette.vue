<script setup lang="ts">
import { nextTick, ref, watch } from "vue";

import { commandScrollTop } from "./command-scroll";
import type { ChatCommandSpec } from "./state";

/**
 * The slash palette: the commands the server accepts, floating above the
 * composer. It is the only command surface -- there is no second list anywhere
 * in the panel -- so the arrow keys and the pointer both land here.
 */
const props = defineProps<{ matches: ChatCommandSpec[]; selection: number }>();
const emit = defineEmits<{ choose: [index: number] }>();

const menu = ref<HTMLElement | null>(null);

// Arrow keys move the selection, and the list scrolls so the highlight stays
// visible once the matches run past the rows that fit. The arithmetic is
// command-scroll.ts's; this only applies what it returns. flush: 'post' so the
// option being measured is the one just selected, not the one leaving.
watch(
  () => [props.selection, props.matches.length],
  async () => {
    await nextTick();
    const el = menu.value;
    const option = el?.children[props.selection] as HTMLElement | undefined;
    if (!el || !option) return;
    el.scrollTop = commandScrollTop({
      scrollTop: el.scrollTop,
      viewportHeight: el.clientHeight,
      optionTop: option.offsetTop,
      optionHeight: option.offsetHeight,
    });
  },
  { flush: "post" },
);
</script>

<template>
  <div
    ref="menu"
    role="listbox"
    aria-label="Commands"
    class="absolute inset-x-3 bottom-[calc(100%-0.25rem)] z-30 grid max-h-72 gap-0.5 overflow-auto rounded-lg border border-border-strong bg-popover p-1.5 shadow-md"
  >
    <button
      v-for="(spec, index) in matches"
      :key="spec.command"
      type="button"
      role="option"
      :aria-selected="index === selection"
      class="grid grid-cols-[minmax(8rem,14rem)_1fr] items-center gap-2.5 rounded-md border border-transparent px-2.5 py-2 text-left text-fg-muted hover:bg-muted hover:text-foreground aria-selected:border-border aria-selected:bg-muted aria-selected:text-foreground"
      @mousedown.prevent
      @click="emit('choose', index)"
    >
      <code class="truncate text-foreground">{{ spec.command }}</code>
      <span class="truncate">{{ spec.description || spec.usage || "" }}</span>
    </button>
  </div>
</template>
