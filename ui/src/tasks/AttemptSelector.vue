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
 * One attempt renders the rail anyway, disabled: the control states "there is
 * exactly one run" by being inert rather than by disappearing, which is what
 * the old text fact ("attempt 1 of 1") said without saying it as navigation.
 */
const run = useTaskRun();

const attempts = computed(() => run.attempts?.attempts ?? []);
const selected = computed(() =>
  attempts.value.findIndex((a) => Number(a.attempt) === Number(run.attemptNumber)),
);

// reka's pagination counts pages over items; one attempt per page makes the
// page number an index into the run history. The controlled page keeps the
// URL (`?attempt=N`) the single source of truth.
const page = computed(() => (selected.value >= 0 ? selected.value + 1 : undefined));

function select(p: number): void {
  const attempt = attempts.value[p - 1];
  if (attempt) run.selectAttempt(Number(attempt.attempt));
}
</script>

<template>
  <Pagination
    v-if="attempts.length && selected >= 0"
    :items-per-page="1"
    :total="attempts.length"
    :page="page"
    :sibling-count="1"
    aria-label="Attempts of this task"
    class="mx-0"
    @update:page="select"
  >
    <PaginationContent>
      <PaginationPrevious size="icon-sm" class="border-transparent" />
      <PaginationList v-slot="{ items }">
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
      </PaginationList>
      <PaginationNext size="icon-sm" class="border-transparent" />
    </PaginationContent>
  </Pagination>
</template>