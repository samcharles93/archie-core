<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ago } from "@/lib/format";
import CapturePayload from "./CapturePayload.vue";
import { selected } from "./state";

/**
 * The selected capture's payload and headers, beside the list.
 *
 * A pane rather than a sheet: a sheet covers the list it was opened from and
 * has to be dismissed between rows, while an inspector is read by moving down
 * the list with the payload in view. On the wide layout it sticks beside the
 * list and scrolls internally, so a long payload cannot take the page with it;
 * stacked under the list, a selection brings it into view, because a tap that
 * changes something off-screen reads as a tap that did nothing.
 *
 * Nothing here goes stale: a stored capture is never rewritten.
 */
const pane = ref<HTMLElement | null>(null);

const meta = computed(() => {
  const capture = selected.value;
  if (!capture) return "";
  return [ago(capture.received_at), capture.content_type || "no content type", capture.remote_addr]
    .filter(Boolean)
    .join(" · ");
});

// Below the split's breakpoint the pane sits after the list, so a row click has
// to bring it into view. Same breakpoint as the grid in CapturesPage: one
// layout decision, expressed twice because CSS cannot hand it to script.
const STACKED = "(max-width: 1099px)";

watch(selected, () => {
  if (!window.matchMedia(STACKED).matches) return;
  pane.value?.scrollIntoView({ block: "start", behavior: "smooth" });
});
</script>

<template>
  <div v-if="selected" ref="pane" class="min-w-0 min-[1100px]:sticky min-[1100px]:top-4">
    <Card class="min-[1100px]:max-h-[calc(100vh-3rem)] min-[1100px]:overflow-y-auto">
      <CardHeader>
        <CardTitle class="font-mono">{{ selected.source || "Unknown source" }}</CardTitle>
        <CardDescription>{{ meta }}</CardDescription>
      </CardHeader>
      <CardContent class="flex flex-col gap-5">
        <CapturePayload label="Payload" :raw="selected.body" />
        <CapturePayload label="Headers" :raw="selected.headers" />
      </CardContent>
    </Card>
  </div>
</template>
