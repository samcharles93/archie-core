<script setup lang="ts">
import { onMounted, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import CaptureDetail from "./CaptureDetail.vue";
import CapturesCard from "./CapturesCard.vue";
import CapturesHeader from "./CapturesHeader.vue";
import { load, selectById, selectNewest, selected, useCaptures } from "./state";

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
 */
useCaptures();

const route = useRoute();
const router = useRouter();

onMounted(async () => {
  await load();
  // The URL names a selection before the window is read, so the restore waits
  // for the list; an id it no longer holds falls back to the newest capture,
  // which is what a page opened with nothing named starts on too.
  const named = Number(route.query.capture);
  if (!Number.isInteger(named) || !selectById(named)) selectNewest();
});

watch(selected, (capture) => {
  const query = { ...route.query };
  if (capture) query.capture = String(capture.id);
  else delete query.capture;
  void router.replace({ query });
});
</script>

<template>
  <CapturesHeader />
  <div class="grid min-w-0 items-start gap-4 min-[1100px]:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
    <CapturesCard class="min-w-0" />
    <CaptureDetail />
  </div>
</template>
