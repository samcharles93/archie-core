<script setup lang="ts">
import CaptureDetailSheet from "./CaptureDetailSheet.vue";
import CapturesCard from "./CapturesCard.vue";
import CapturesHeader from "./CapturesHeader.vue";
import { useCaptures } from "./state";

/**
 * Event inspector (t2db.2): the operator-facing half of capture. Recent
 * inbound events, newest first, with the full redacted payload explorable from
 * each row -- see docs/prds/event-capture-storage.md for what the backend
 * guarantees (retention, redaction, disk bounds). Composes the page; it holds
 * no state of its own.
 */
useCaptures();
</script>

<template>
  <CapturesHeader />
  <CapturesCard />
  <CaptureDetailSheet />
</template>
