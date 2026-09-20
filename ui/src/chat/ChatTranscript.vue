<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import ChatBubble from "./ChatBubble.vue";
import ChatEmptyState from "./ChatEmptyState.vue";
import { messages, sendMessage, streamingTurn, type ChatMessage } from "./state";

/**
 * The conversation: the messages so far, the turn still arriving, and what the
 * panel says before either exists. It scrolls itself to the newest line, so a
 * streamed answer stays in view as it grows.
 */
const emit = defineEmits<{ prompt: [text: string] }>();

const scroller = ref<{ $el: HTMLElement } | null>(null);

function parts(message: ChatMessage) {
  const from = message.from ?? message.From ?? "";
  // Anything this browser's composer did not send is Archie's answer.
  return {
    text: message.text ?? message.Text ?? "",
    tools: message.tool_calls || message.ToolCalls || [],
    assistant: from !== "web",
  };
}

const bubbles = computed(() => messages.value.map(parts));

// ScrollArea renders its own scrollable element, so the way to follow the tail
// is to reach into the component for it. Asking the wrapper instead would set
// scrollTop on a box that does not scroll.
function viewport(): HTMLElement | null {
  return scroller.value?.$el?.querySelector<HTMLElement>('[data-slot="scroll-area-viewport"]') ?? null;
}

// Auto scroll to the bottom on a new message or a streamed token. flush: 'post'
// measures after the new content is in the DOM; measuring before that reads the
// height the transcript is about to replace.
watch(
  [messages, streamingTurn],
  () => {
    const el = viewport();
    if (el) el.scrollTop = el.scrollHeight;
  },
  { flush: "post" },
);
</script>

<template>
  <ScrollArea ref="scroller" class="min-h-0 flex-1">
    <div class="grid content-start gap-4 p-4">
      <ChatEmptyState v-if="!bubbles.length && !streamingTurn" @prompt="emit('prompt', $event)" />

      <template v-else>
        <ChatBubble
          v-for="(bubble, i) in bubbles"
          :key="i"
          :text="bubble.text"
          :tools="bubble.tools"
          :assistant="bubble.assistant"
        />

        <!-- The live turn is a bubble like any other, so what arrives is
             rendered by the same rules as what was already there. -->
        <ChatBubble
          v-if="streamingTurn"
          :text="streamingTurn.text || '…'"
          :tools="streamingTurn.tools"
          assistant
          streaming
        >
          <Button
            v-if="streamingTurn.isError && streamingTurn.turn"
            variant="outline"
            size="sm"
            class="mt-2"
            @click="sendMessage({ turn: streamingTurn.turn })"
          >
            Retry
          </Button>
        </ChatBubble>
      </template>
    </div>
  </ScrollArea>
</template>
