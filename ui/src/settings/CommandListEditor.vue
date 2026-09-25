<script setup lang="ts">
import { Plus, Trash2 } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import {
  TagsInput,
  TagsInputInput,
  TagsInputItem,
  TagsInputItemDelete,
  TagsInputItemText,
} from "@/components/ui/tags-input";

/** A list of commands, each an argv array: one tag per argument, so nothing
 * is ever re-split by a shell. */
const props = defineProps<{ label: string; emptyText?: string }>();
const commands = defineModel<string[][] | null>({ required: true });

function add() {
  commands.value = [...(commands.value ?? []), []];
}
function remove(i: number) {
  const next = [...(commands.value ?? [])];
  next.splice(i, 1);
  commands.value = next;
}
function set(i: number, argv: string[]) {
  const next = [...(commands.value ?? [])];
  next[i] = argv;
  commands.value = next;
}
</script>

<template>
  <div class="grid gap-2">
    <p v-if="!commands?.length && props.emptyText" class="text-xs text-fg-subtle">{{ props.emptyText }}</p>
    <div v-for="(argv, i) in commands ?? []" :key="i" class="flex items-start gap-2">
      <span class="w-5 pt-2 text-right font-mono text-xs text-fg-subtle">{{ i + 1 }}</span>
      <TagsInput
        :model-value="argv"
        duplicate
        :delimiter="'\u0000'"
        class="flex-1 font-mono"
        :aria-label="`${props.label} command ${i + 1}`"
        @update:model-value="(v) => set(i, v as string[])"
      >
        <TagsInputItem v-for="(arg, j) in argv" :key="`${j}-${arg}`" :value="arg">
          <TagsInputItemText />
          <TagsInputItemDelete />
        </TagsInputItem>
        <TagsInputInput placeholder="argument" />
      </TagsInput>
      <Button variant="ghost" size="icon" :aria-label="`Remove ${props.label} command ${i + 1}`" @click="remove(i)"><Trash2 /></Button>
    </div>
    <div>
      <Button variant="outline" size="sm" @click="add"><Plus data-icon="inline-start" /> Add command</Button>
    </div>
  </div>
</template>
