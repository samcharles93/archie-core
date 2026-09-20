<script setup lang="ts">
import { ref } from "vue";

import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { promptSessionCommand, runSessionCommand, useEscapeLayer } from "./state";

/**
 * The commands that act on the conversation itself rather than on what is being
 * said: retry the last turn, undo it, rename the conversation, branch it, or
 * look at what compressing it would drop.
 *
 * Each one is the same command the server accepts from any other channel, so
 * this is a set of buttons over the slash surface rather than a second way to
 * do the work. The popover closes as the command goes, because its answer comes
 * back into the transcript and the status line, not into the menu.
 */
const open = ref(false);
useEscapeLayer(open);

function run(command: string): void {
  open.value = false;
  void runSessionCommand(command);
}

function prompt(command: string, message: string): void {
  open.value = false;
  promptSessionCommand(command, message);
}
</script>

<template>
  <div class="flex items-center border-b px-3 py-1.5 text-xs">
    <Popover v-model:open="open">
      <PopoverTrigger as-child>
        <Button variant="ghost" size="xs" class="text-fg-muted">Session actions</Button>
      </PopoverTrigger>
      <PopoverContent align="start" class="flex w-[min(22rem,72vw)] flex-wrap gap-1.5">
        <Button variant="outline" size="sm" @click="run('/retry')">Retry</Button>
        <Button variant="outline" size="sm" @click="run('/undo')">Undo</Button>
        <Button variant="outline" size="sm" @click="prompt('/title', 'Session title')">Rename</Button>
        <Button variant="outline" size="sm" @click="prompt('/branch', 'Branch name (optional)')">Branch</Button>
        <Button variant="outline" size="sm" @click="run('/compress --preview')">Preview compression</Button>
      </PopoverContent>
    </Popover>
  </div>
</template>
