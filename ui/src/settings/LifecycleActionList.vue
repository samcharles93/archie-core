<script setup lang="ts">
import { Button, type ButtonVariants } from "@/components/ui/button";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import ConfigList from "./ConfigList.vue";
import ConfigRow from "./ConfigRow.vue";
import type { LifecycleEntry } from "./types";

/**
 * The operator actions archied offers, shown as the control each one renders
 * as. They are disabled here: this is the vocabulary, not a task to act on,
 * and every one of them needs a task to mean anything.
 */
const props = defineProps<{ actions: LifecycleEntry[] }>();

/** A quiet action reads as a plain control, a dangerous one as a destructive
 * one, so the list shows the weight each action carries. */
function actionVariant(kind?: string): ButtonVariants["variant"] {
  if (kind === "quiet") return "ghost";
  if (kind === "danger") return "destructive";
  return "outline";
}
</script>

<template>
  <h3 class="mt-5 mb-2 text-sm font-semibold text-fg-muted">Actions</h3>
  <Empty v-if="!props.actions.length">
    <EmptyHeader>
      <EmptyTitle>No actions reported</EmptyTitle>
    </EmptyHeader>
  </Empty>
  <ConfigList v-else>
    <ConfigRow v-for="action in props.actions" :key="action.id" :label="action.id">
      <template #value>
        <span>
          <Button :variant="actionVariant(action.kind)" size="xs" disabled>{{ action.label ?? action.id }}</Button>
        </span>
      </template>
    </ConfigRow>
  </ConfigList>
</template>
