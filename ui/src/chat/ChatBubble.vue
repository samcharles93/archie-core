<script setup lang="ts">
import { cn } from "@/lib/utils";
import ChatMarkdown from "./ChatMarkdown.vue";
import ChatToolCall from "./ChatToolCall.vue";
import type { ChatToolCall as ToolCall } from "./state";

/**
 * One message, from either side of the conversation. There is no speaker
 * label: which side of the panel it sits on, and its fill, are what say who is
 * talking. A trailing action arrives through the slot, so the retry the
 * transcript offers belongs to the bubble rather than beside it.
 */
withDefaults(
  defineProps<{
    text: string;
    tools?: ToolCall[];
    assistant?: boolean;
    /** The turn is still arriving: a tool call with no outcome is running. */
    streaming?: boolean;
  }>(),
  { tools: () => [], assistant: false, streaming: false },
);
</script>

<template>
  <div class="flex" :class="assistant ? 'justify-start' : 'justify-end'">
    <div
      :class="
        cn(
          'max-w-[min(78ch,86%)] rounded-[0.85rem] px-4 py-3 leading-normal [overflow-wrap:anywhere]',
          assistant ? 'bg-muted' : 'bg-primary/15',
        )
      "
    >
      <div v-if="assistant && tools.length" class="mb-2 grid gap-0.5 text-xs">
        <ChatToolCall
          v-for="(tool, i) in tools"
          :key="i"
          :tool="tool"
          :streaming="streaming"
        />
      </div>
      <ChatMarkdown v-if="assistant" :text="text" />
      <div v-else class="whitespace-pre-wrap">{{ text }}</div>
      <slot />
    </div>
  </div>
</template>
