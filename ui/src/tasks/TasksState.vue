<script setup lang="ts">
import { computed } from "vue";

import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import { Spinner } from "@/components/ui/spinner";

/**
 * What the board says when it has no rows to draw: still fetching, unreachable,
 * nothing picked up yet, or nothing matching the filters. One component for all
 * four, so no surface invents its own wording for the same situation.
 */
type StateKind = "loading" | "error" | "empty" | "no-match";

const props = defineProps<{ kind: StateKind; detail?: string | null }>();

const emit = defineEmits<{ clear: [] }>();

const copy = computed(() => {
  if (props.kind === "error") {
    return {
      title: "Cannot reach archied",
      description: props.detail || "The daemon did not answer.",
    };
  }
  if (props.kind === "empty") {
    return {
      title: "No tasks yet",
      description: "Tasks appear here once archied picks up an issue to work.",
    };
  }
  if (props.kind === "no-match") {
    return {
      title: "No matching tasks",
      description: "Try a different search or status filter.",
    };
  }
  return { title: "Loading tasks…", description: "" };
});
</script>

<template>
  <div
    v-if="props.kind === 'loading'"
    class="text-fg-muted flex items-center justify-center gap-2 p-6 text-sm"
  >
    <Spinner />
    {{ copy.title }}
  </div>
  <Empty v-else>
    <EmptyHeader>
      <EmptyTitle>{{ copy.title }}</EmptyTitle>
      <EmptyDescription>{{ copy.description }}</EmptyDescription>
    </EmptyHeader>
    <EmptyContent v-if="props.kind === 'no-match'">
      <Button variant="ghost" size="sm" @click="emit('clear')"
        >Clear filters</Button
      >
    </EmptyContent>
  </Empty>
</template>
