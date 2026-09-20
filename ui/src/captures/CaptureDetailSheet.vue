<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { ago } from "@/lib/format";
import CapturePayload from "./CapturePayload.vue";
import { selected, type Capture } from "./state";

/**
 * One capture's payload and headers, opened from its row. A side panel rather
 * than a taller row: a payload runs to thousands of characters, and the list
 * has to stay a list while it is read.
 */

const open = computed({
  get: () => selected.value !== null,
  set: (value: boolean) => {
    if (!value) selected.value = null;
  },
});

// What the panel renders. The list drops the selection as soon as this sheet
// reports closed, and the panel stays mounted through the close animation, so
// reading the shared ref would blank it halfway out. Nothing goes stale here:
// a stored capture is never rewritten.
const shown = ref<Capture | null>(null);
watch(selected, (next) => {
  if (next) shown.value = next;
});
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="w-full gap-0 sm:max-w-2xl">
      <SheetHeader>
        <SheetTitle>{{ shown?.source || "Unknown source" }}</SheetTitle>
        <SheetDescription>
          {{ ago(shown?.received_at) }} · {{ shown?.content_type || "no content type" }}
        </SheetDescription>
      </SheetHeader>
      <Separator />
      <ScrollArea class="min-h-0 flex-1">
        <div class="flex flex-col gap-5 p-4">
          <CapturePayload label="Payload" :raw="shown?.body" />
          <CapturePayload label="Headers" :raw="shown?.headers" />
        </div>
      </ScrollArea>
    </SheetContent>
  </Sheet>
</template>
