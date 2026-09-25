<script setup lang="ts">
import { computed, useId } from "vue";
import { Trash2 } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { Switch } from "@/components/ui/switch";
import {
  TagsInput,
  TagsInputInput,
  TagsInputItem,
  TagsInputItemDelete,
  TagsInputItemText,
} from "@/components/ui/tags-input";

export interface McpServer {
  name: string;
  transport: string;
  command?: string;
  args?: string[];
  work_dir?: string;
  url?: string;
  sse_endpoint?: string;
  message_endpoint?: string;
  parallel_tool_calls: boolean;
  headers_configured: boolean;
}

const server = defineModel<McpServer>({ required: true });
defineEmits<{ remove: [] }>();
const id = useId();

// "" and "stdio" are the same transport to the daemon; "streamablehttp" is
// the long spelling of "http".
const transport = computed({
  get: () =>
    server.value.transport === "" ? "stdio" : server.value.transport === "streamablehttp" ? "http" : server.value.transport,
  set: (value: string) => (server.value.transport = value),
});
const transports = [
  { value: "stdio", label: "stdio" },
  { value: "http", label: "HTTP" },
  { value: "sse", label: "SSE" },
];
const args = computed({
  get: () => server.value.args ?? [],
  set: (value: string[]) => (server.value.args = value),
});
</script>

<template>
  <div class="grid gap-4 rounded-lg border border-border bg-card p-4">
    <div class="flex items-center gap-3">
      <Input
        :id="`${id}-name`"
        v-model="server.name"
        aria-label="Server name"
        placeholder="name"
        class="max-w-56 font-mono"
      />
      <SegmentedControl v-model="transport" label="Transport" :options="transports" />
      <Button variant="ghost" size="icon" class="ml-auto" aria-label="Remove server" @click="$emit('remove')">
        <Trash2 />
      </Button>
    </div>

    <template v-if="transport === 'stdio'">
      <label class="grid gap-1 text-xs text-fg-subtle">
        Command
        <Input v-model="server.command" class="font-mono" placeholder="npx" />
      </label>
      <div class="grid gap-1 text-xs text-fg-subtle">
        <span :id="`${id}-args`">Arguments</span>
        <TagsInput v-model="args" :aria-labelledby="`${id}-args`" class="font-mono">
          <TagsInputItem v-for="arg in args" :key="arg" :value="arg">
            <TagsInputItemText />
            <TagsInputItemDelete />
          </TagsInputItem>
          <TagsInputInput placeholder="Add argument" />
        </TagsInput>
      </div>
      <label class="grid gap-1 text-xs text-fg-subtle">
        Working directory
        <Input v-model="server.work_dir" class="font-mono" placeholder="the daemon's working directory" />
      </label>
    </template>
    <label v-else-if="transport === 'http'" class="grid gap-1 text-xs text-fg-subtle">
      URL
      <Input v-model="server.url" class="font-mono" placeholder="https://host/mcp" />
    </label>
    <template v-else>
      <label class="grid gap-1 text-xs text-fg-subtle">
        SSE endpoint
        <Input v-model="server.sse_endpoint" class="font-mono" />
      </label>
      <label class="grid gap-1 text-xs text-fg-subtle">
        Message endpoint
        <Input v-model="server.message_endpoint" class="font-mono" />
      </label>
    </template>

    <div class="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
      <label class="flex items-center gap-2">
        <Switch v-model="server.parallel_tool_calls" />
        Parallel tool calls
      </label>
      <span v-if="server.headers_configured" class="text-xs text-fg-subtle">
        Headers set in config.toml
      </span>
    </div>
  </div>
</template>
