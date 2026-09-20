<script setup lang="ts">
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";

/**
 * The list's nothing-to-show state: a title that names the situation and a
 * line saying what to expect next. Which words those are is decided by
 * logs-empty.ts, not here.
 */
defineProps<{ title: string; detail?: string }>();
</script>

<template>
  <Empty>
    <EmptyHeader>
      <EmptyTitle>{{ title }}</EmptyTitle>
      <EmptyDescription v-if="detail">{{ detail }}</EmptyDescription>
    </EmptyHeader>
  </Empty>
</template>
