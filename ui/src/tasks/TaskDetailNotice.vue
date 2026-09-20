<script setup lang="ts">
import { ArrowLeft } from "@lucide/vue";
import { computed } from "vue";
import { RouterLink } from "vue-router";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";

/**
 * A run page that cannot be opened, said plainly. There are two ways in: the
 * id in the path is not a task id at all, or archied answers that no such task
 * exists. Both are one page head and one card, so they are one component and
 * only the words differ.
 */
const props = defineProps<{
  kind: "invalid" | "missing";
  /** The id as it arrived in the path. */
  raw?: string;
}>();

const copy = computed(() =>
  props.kind === "invalid"
    ? {
        title: "Not a task id",
        sub: "The run view addresses one task by its id: /tasks/42",
        empty: `“${props.raw ?? ""}” is not a task id`,
        detail: "A task id is a positive whole number. Nothing was requested.",
      }
    : {
        title: "Task not found",
        sub: `No task with id ${props.raw ?? ""} in this deployment.`,
        empty: `Nothing is recorded for task ${props.raw ?? ""}`,
        detail: "archied answered that this task does not exist.",
      },
);
</script>

<template>
  <div>
    <div class="mb-5 flex flex-wrap items-start justify-between gap-5">
      <div>
        <h1 class="text-3xl font-semibold tracking-[-0.03em]">{{ copy.title }}</h1>
        <p class="mt-2 text-sm text-fg-muted">{{ copy.sub }}</p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <Button variant="outline" as-child>
          <RouterLink to="/tasks">
            <ArrowLeft data-icon="inline-start" />
            Back to tasks
          </RouterLink>
        </Button>
      </div>
    </div>
    <Card>
      <CardContent>
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{{ copy.empty }}</EmptyTitle>
            <EmptyDescription>{{ copy.detail }}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </CardContent>
    </Card>
  </div>
</template>
