<script setup lang="ts">
import { Wrench } from "@lucide/vue";
import { computed } from "vue";

import { cn } from "@/lib/utils";
import ChatNavChip from "./ChatNavChip.vue";
import type { ChatToolCall } from "./state";

/**
 * One tool call: the name, the parameters it was given, and what came back.
 *
 * A quiet log line rather than the point of the reply: it is evidence for the
 * answer under it, so it is smaller and dimmer than the text it produced, and
 * a failed call is the only thing here that raises its voice. An empty list
 * renders nothing, so a turn that called no tools looks untouched.
 */
const props = defineProps<{
  tool: ChatToolCall;
  /** The turn is still arriving, so a call with no outcome yet is running. */
  streaming?: boolean;
}>();

const name = computed(() => props.tool.name || props.tool.Name || props.tool.tool || "tool");
const parameters = computed(() => props.tool.parameters || props.tool.Parameters || "");
const failed = computed(() => Boolean(props.tool.err || props.tool.Err || props.tool.failed));

// A recorded turn reports its outcome in summary, a live frame in text, and a
// failure may only carry the error. Which words stand in for silence depends on
// whether the turn is still going: "running…" is true mid-turn and a lie after.
const summary = computed(() => {
  const reported = props.tool.summary || props.tool.Summary || props.tool.text || props.tool.err || props.tool.Err;
  if (reported) return reported;
  if (!props.streaming) return "done";
  return failed.value ? "failed" : "running…";
});
</script>

<template>
  <ChatNavChip v-if="tool.path" :path="tool.path" :label="tool.label" />
  <div v-else class="flex items-baseline gap-1.5 [overflow-wrap:anywhere]">
    <Wrench aria-hidden="true" class="size-3.5 shrink-0 self-center text-fg-subtle" />
    <span class="font-semibold text-foreground">{{ name }}</span>
    <code v-if="parameters" class="font-mono text-[0.95em] text-fg-subtle">{{ parameters }}</code>
    <span :class="cn('text-fg-muted', failed && 'text-danger')">{{ summary }}</span>
  </div>
</template>
