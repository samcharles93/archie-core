<script setup lang="ts">
import { useMediaQuery } from "@vueuse/core";
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
 * the list with the payload in view.
 *
 * `maxHeight` is the cap the page worked out -- the list's height, or the room
 * the window has, whichever is smaller -- so a payload of any size stays a
 * payload beside the list instead of becoming a page of its own. Its title sits
 * outside the scrolling area: what an operator scrolls back to is the top of
 * the record, not the top of the panel.
 *
 * Stacked under the list there is no cap to fill, so a selection brings the
 * pane into view instead -- a tap that changes something off-screen reads as a
 * tap that did nothing.
 *
 * Nothing here goes stale: a stored capture is never rewritten.
 */
const props = defineProps<{ maxHeight?: number | null }>();

const pane = ref<HTMLElement | null>(null);

// The pane is in its stacked layout exactly when the grid is: same breakpoint
// as CapturesPage, matched there in CSS and here in script.
const stacked = useMediaQuery("(max-width: 1099px)");

const cap = computed(() => (stacked.value || !props.maxHeight ? undefined : `${props.maxHeight}px`));

const meta = computed(() => {
  const capture = selected.value;
  if (!capture) return "";
  return [ago(capture.received_at), capture.content_type || "no content type", capture.remote_addr]
    .filter(Boolean)
    .join(" · ");
});

watch(selected, () => {
  if (!stacked.value) return;
  pane.value?.scrollIntoView({ block: "start", behavior: "smooth" });
});
</script>

<template>
  <div v-if="selected" ref="pane" class="min-w-0">
    <Card class="flex flex-col" :style="cap ? { maxHeight: cap } : undefined">
      <CardHeader>
        <CardTitle class="font-mono">{{ selected.source || "Unknown source" }}</CardTitle>
        <CardDescription>{{ meta }}</CardDescription>
      </CardHeader>
      <!-- Stacked there is no sibling to be bound by, so the panel caps itself
           the way the task log does rather than letting one large payload
           become a ten-thousand-pixel page. Split, the page's cap is the bound
           and this one is lifted. -->
      <CardContent class="min-h-0 max-h-[62vh] overflow-y-auto min-[1100px]:max-h-none">
        <div class="flex flex-col gap-5">
          <CapturePayload label="Payload" :raw="selected.body" />
          <CapturePayload label="Headers" :raw="selected.headers" />
        </div>
      </CardContent>
    </Card>
  </div>
</template>
