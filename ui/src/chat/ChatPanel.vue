<script setup lang="ts">
import { computed, ref } from "vue";

import ChatBar from "./ChatBar.vue";
import ChatComposer from "./ChatComposer.vue";
import ChatHeader from "./ChatHeader.vue";
import ChatSessionActions from "./ChatSessionActions.vue";
import ChatTranscript from "./ChatTranscript.vue";
import { CHAT_PANEL_ID, composerText, currentSession, useChat } from "./state";

/**
 * The panel: the shape that grows out of the launcher, holding the
 * conversation. It composes the head, the bar, the transcript and the composer,
 * and owns nothing but the morph and the composer's focus.
 *
 * It stays mounted while closed, so the session and any turn in flight survive
 * a navigation. `inert` is what keeps an invisible panel out of the tab order
 * and out of the pointer's way.
 */
const props = defineProps<{ open: boolean }>();

useChat();

const composer = ref<InstanceType<typeof ChatComposer> | null>(null);

/**
 * The panel's shape.
 *
 * Closed, it is collapsed to a small round shape sitting exactly over the
 * button; open, it is the full panel beside it. Scaling from the bottom-right
 * corner and translating by the launcher's own width is what makes the button
 * appear to become the panel, rather than a panel merely fading in next to it:
 * scale, border-radius and position interpolate together. The contents fade in
 * separately, below, once the shape has arrived.
 *
 * On a phone the sheet rises from the launcher instead of opening beside it, so
 * the closed shape travels down toward the button rather than sideways. Those
 * two values (56px launcher, 0.75rem gap) are the dock's own geometry; the
 * launcher and every offset here are built from them.
 */
const panelShape = computed(() =>
  props.open
    ? "pointer-events-auto rounded-lg opacity-100 [transform:none] [transition:opacity_160ms_cubic-bezier(0.4,0,0.2,1),transform_260ms_cubic-bezier(0.22,1,0.36,1),border-radius_260ms_cubic-bezier(0.22,1,0.36,1)]"
    : "pointer-events-none rounded-full opacity-0 [transform:translateX(calc(56px_+_0.75rem))_scale(0.16)] max-md:[transform:translateY(calc(56px_+_0.75rem))_scale(0.16)] [transition:opacity_140ms_cubic-bezier(0.4,0,0.2,1),transform_200ms_cubic-bezier(0.4,0,1,1),border-radius_200ms_cubic-bezier(0.4,0,1,1)]",
);

// The contents follow the shape rather than stretching with it: they fade in
// once the panel is most of the way open, so the morph reads as a container
// opening instead of a page being scaled up.
const contentFade = computed(() =>
  props.open
    ? "opacity-100 transition-opacity duration-[160ms] ease-linear delay-[120ms]"
    : "opacity-0 transition-opacity duration-[120ms] ease-linear",
);

function usePrompt(text: string): void {
  composerText.value = text;
  composer.value?.focus();
}
</script>

<template>
  <aside
    :id="CHAT_PANEL_ID"
    aria-label="Chat with Archie"
    :aria-hidden="open ? undefined : 'true'"
    class="pointer-events-none absolute bottom-0 right-[calc(56px_+_0.75rem)] h-[min(640px,calc(100vh_-_3rem))] w-[min(440px,calc(100vw_-_56px_-_0.75rem_-_3rem))] max-md:fixed max-md:bottom-[calc(1rem_+_56px_+_0.75rem)] max-md:left-4 max-md:right-4 max-md:h-[min(72vh,calc(100vh_-_2rem_-_56px_-_0.75rem))] max-md:w-auto"
  >
    <div
      :class="
        panelShape +
        ' h-full w-full overflow-hidden border bg-popover shadow-[var(--shadow-md)] origin-bottom-right'
      "
      :inert="open ? undefined : true"
    >
      <!-- The conversation takes the rest of the column: without `min-h-0` on
           the transcript it sizes to its content instead, and the composer
           floats above dead space. -->
      <div
        :class="contentFade + ' flex h-full min-h-0 flex-col overflow-hidden'"
      >
        <ChatHeader />
        <ChatBar />
        <ChatSessionActions v-if="currentSession" />
        <ChatTranscript @prompt="usePrompt" />
        <ChatComposer ref="composer" :open="open" />
      </div>
    </div>
  </aside>
</template>
