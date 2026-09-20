<script setup lang="ts">
import { computed } from "vue";

import { Button } from "@/components/ui/button";
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from "@/components/ui/message-scroller";
import ChatBubble from "./ChatBubble.vue";
import ChatEmptyState from "./ChatEmptyState.vue";
import { messages, sendMessage, streamingTurn, type ChatMessage } from "./state";

/**
 * The conversation: the messages so far, the turn still arriving, and what the
 * panel says before either exists.
 *
 * The scroller follows the tail, and stops following the moment the reader
 * scrolls up rather than yanking them back on every streamed token. That is
 * what MessageScroller is for, so it is used rather than re-derived: an
 * earlier version set scrollTop from a watcher, which has neither the
 * "reader scrolled away" state nor a way back down.
 */
const emit = defineEmits<{ prompt: [text: string] }>();

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
</script>

<template>
  <MessageScrollerProvider auto-scroll default-scroll-position="end">
    <MessageScroller class="min-h-0 flex-1">
      <MessageScrollerViewport>
        <MessageScrollerContent class="gap-4 p-4">
          <ChatEmptyState v-if="!bubbles.length && !streamingTurn" @prompt="emit('prompt', $event)" />

          <template v-else>
            <MessageScrollerItem v-for="(bubble, i) in bubbles" :key="i" :message-id="`m${i}`">
              <ChatBubble :text="bubble.text" :tools="bubble.tools" :assistant="bubble.assistant" />
            </MessageScrollerItem>

            <!-- The live turn is a bubble like any other, so what arrives is
                 rendered by the same rules as what was already there. It is the
                 scroll anchor while it streams. -->
            <MessageScrollerItem v-if="streamingTurn" message-id="streaming" scroll-anchor>
              <ChatBubble
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
            </MessageScrollerItem>
          </template>
        </MessageScrollerContent>
      </MessageScrollerViewport>

      <MessageScrollerButton />
    </MessageScroller>
  </MessageScrollerProvider>
</template>
