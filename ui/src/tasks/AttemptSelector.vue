<script setup lang="ts">
import { computed } from "vue";

import {
  Pagination,
  PaginationContent,
  PaginationEllipsis,
  PaginationItem,
  PaginationNext,
  PaginationPrevious,
} from "@/components/ui/pagination";
import { useTaskRun } from "./use-task-run";

/**
 * The attempts of this task as a pager, sitting above the tab card: chevrons
 * walk run history, the numbers land on an attempt directly, and the attempt
 * on screen reads as the active page. Selection is the page's, and the stage
 * rail reads the same response, so the two views of the same task's attempts
 * can never disagree about which one is on screen.
 *
 * Hidden for zero or one attempt: a one-page pager is the text fact wearing
 * extra chrome. The URL names the attempt either way, and the panels show
 * which attempt is on screen.
 */
const run = useTaskRun();

const attempts = computed(() => run.attempts?.attempts ?? []);

const selected = computed(() =>
  attempts.value.findIndex(
    (a) => Number(a.attempt) === Number(run.attemptNumber),
  ),
);

const loaded = computed(() => attempts.value.length > 1);
const total = computed(() => attempts.value.length);

// reka's pagination counts pages over items; one attempt per page makes the
// page number an index into the run history. The controlled page keeps the
// URL (`?attempt=N`) the single source of truth.
const page = computed(() => (selected.value >= 0 ? selected.value + 1 : 1));

function select(p: number): void {
  const attempt = attempts.value[p - 1];
  if (attempt) run.selectAttempt(Number(attempt.attempt));
}
</script>

<template>
  <Pagination
    v-if="loaded"
    :items-per-page="1"
    :total="total"
    :page="page"
    :sibling-count="1"
    aria-label="Attempts of this task"
    class="mx-0 w-auto"
    @update:page="select"
  >
    <PaginationContent v-slot="{ items }">
      <PaginationPrevious size="icon-sm" class="border-transparent" />
      <template v-for="(item, i) in items" :key="i">
        <PaginationEllipsis v-if="item.type === 'ellipsis'" :index="i" />
        <PaginationItem
          v-else
          :value="item.value"
          :is-active="item.value === page"
          size="icon-sm"
          :title="`Attempt ${attempts[item.value - 1]?.attempt}`"
        >
          {{ item.value }}
        </PaginationItem>
      </template>
      <PaginationNext size="icon-sm" class="border-transparent" />
    </PaginationContent>
  </Pagination>
</template>
