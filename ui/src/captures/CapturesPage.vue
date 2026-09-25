<script setup lang="ts">
import { breakpointsTailwind, useBreakpoints } from "@vueuse/core";
import { onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import CaptureDetail from "./CaptureDetail.vue";
import CapturesCard from "./CapturesCard.vue";
import EventTypesCard from "./EventTypesCard.vue";
import { loadEventTypes } from "./event-type-state";
import { captures, load, selectById, selectNewest, selected, useCaptures } from "./state";

/**
 * Event inspector: recent inbound events, newest first, with the selected
 * event's payload read beside the list rather than over it -- see
 * docs/prds/event-capture-storage.md for what the backend guarantees
 * (retention, redaction, disk bounds).
 *
 * The selection lives in the address bar (`?capture=7`): a row an operator is
 * reading survives a reload and can be linked to. Selecting is a `replace`, not
 * a push, so walking the list adds no history entries to back out through.
 * Narrow screens stack the pane under the list.
 *
 * The pane is capped by the list it sits beside and by the room the window has
 * for it -- never by its own content, which a payload has plenty of. Both of
 * those are sizes the pane does not influence, so filling the cap cannot feed
 * back into it; the page therefore stays as tall as the list, however large the
 * payload.
 */
useCaptures();

const route = useRoute();
const router = useRouter();

const listColumn = ref<HTMLElement | null>(null);
const paneCap = ref<number | null>(null);

// The same breakpoint the grid states in its classes: one layout decision,
// expressed twice because CSS cannot hand it to script.
const stacked = useBreakpoints(breakpointsTailwind).smaller("lg");

// Below the frame: the room left under the pane's own top edge. A margin
// rather than a measured padding on purpose -- being a few pixels out costs a
// few pixels, where measuring it from a frame that grows with its content
// costs a runaway height.
const FLOOR_MARGIN = 24;
const MIN_PANE = 320;

function measure(): void {
  const column = listColumn.value;
  if (stacked.value || !column) {
    paneCap.value = null;
    return;
  }
  const rect = column.getBoundingClientRect();
  const listHeight = rect.height;
  const room = window.innerHeight - rect.top - FLOOR_MARGIN;
  paneCap.value = Math.max(MIN_PANE, Math.min(listHeight || MIN_PANE, room));
}

let observer: ResizeObserver | null = null;

onMounted(async () => {
  measure();
  if (listColumn.value) {
    observer = new ResizeObserver(measure);
    observer.observe(listColumn.value);
  }
  window.addEventListener("resize", measure);

  await Promise.all([load(), loadEventTypes()]);
  // The URL names a selection before the window is read, so the restore waits
  // for the list; an id it no longer holds falls back to the newest capture,
  // which is what a page opened with nothing named starts on too.
  const named = Number(route.query.capture);
  if (!Number.isInteger(named) || !selectById(named)) selectNewest();
});

onBeforeUnmount(() => {
  observer?.disconnect();
  window.removeEventListener("resize", measure);
});
watch(stacked, measure);

watch(selected, (capture) => {
  const query = { ...route.query };
  if (capture) query.capture = String(capture.id);
  else delete query.capture;
  void router.replace({ query });
});
</script>

<template>
  <!-- Split only when there is a capture to show beside the list. -->
  <div
    class="grid min-w-0 items-start gap-4"
    :class="captures.length ? 'lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]' : ''"
  >
    <div ref="listColumn" class="min-w-0">
      <CapturesCard />
    </div>
    <CaptureDetail :max-height="paneCap" />
  </div>
  <div class="mt-4">
    <EventTypesCard />
  </div>
</template>
