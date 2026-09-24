<script setup lang="ts">
import { ref, watch } from "vue";

import LogRow from "@/base/LogRow.vue";
import { Skeleton } from "@/components/ui/skeleton";

import { entryKey } from "./log-entries";
import LogsEmpty from "./LogsEmpty.vue";
import { emptyDetail, emptyTitle, entries, loading, readError } from "./state";

/**
 * The lines, or the one thing that stands in for them.
 *
 * Dense and monospace by design: the point of this page is scanning a lot of
 * lines quickly.
 */

// Varied widths, so the placeholder reads as lines of a log rather than a
// table waiting to be filled.
const SKELETON_ROWS = [
  "w-11/12",
  "w-3/4",
  "w-5/6",
  "w-2/3",
  "w-11/12",
  "w-1/2",
  "w-5/6",
  "w-3/4",
];

const scroller = ref<HTMLElement | null>(null);

// The tail follows the newest line, which is the last one. flush: 'post'
// scrolls after the new rows are in the DOM -- scrolling before that measures
// the height the list is about to replace.
watch(
  entries,
  () => {
    const el = scroller.value;
    if (el) el.scrollTop = el.scrollHeight;
  },
  { flush: "post" },
);
</script>

<template>
  <div ref="scroller" class="max-h-[62vh] overflow-y-auto font-mono text-xs">
    <!-- A failed read is about this page's request, not about the log, so it
         says what the server said rather than borrowing an empty state. -->
    <LogsEmpty v-if="readError" title="Cannot read logs" :detail="readError" />
    <div
      v-else-if="loading && !entries.length"
      class="flex flex-col gap-3 px-3 py-4"
    >
      <Skeleton
        v-for="(width, i) in SKELETON_ROWS"
        :key="i"
        class="h-3"
        :class="width"
      />
    </div>
    <LogsEmpty
      v-else-if="!entries.length"
      :title="emptyTitle"
      :detail="emptyDetail"
    />
    <template v-else>
      <LogRow v-for="entry in entries" :key="entryKey(entry)" :entry="entry" />
    </template>
  </div>
</template>
