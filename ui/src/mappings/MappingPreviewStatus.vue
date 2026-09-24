<script setup lang="ts">
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import type { MappingField, Preview, PreviewFailure } from "./state";

/**
 * What the last preview said about one field: the value it resolved to, the
 * reason it could not resolve, or an explicit "not previewed".
 *
 * A null preview means nothing has been claimed yet, and a field that was not
 * previewed must never render as though it passed -- an unresolved field is not
 * the same claim as a resolved one.
 */
const props = defineProps<{
  field: MappingField;
  preview: Preview | null;
  failure?: PreviewFailure;
}>();

const status = computed<{ variant: "ok" | "danger" | "idle"; text: string }>(
  () => {
    if (!props.preview) return { variant: "idle", text: "not previewed" };
    if (props.failure) return { variant: "danger", text: props.failure.reason };
    if (
      Object.prototype.hasOwnProperty.call(
        props.preview.values || {},
        props.field.name,
      )
    ) {
      return {
        variant: "ok",
        text: JSON.stringify(props.preview.values?.[props.field.name]),
      };
    }
    // Absent from values and absent from failures: an optional field the payload
    // did not carry, which is not a failure.
    return { variant: "idle", text: "skipped (optional)" };
  },
);
</script>

<template>
  <Badge :variant="status.variant">{{ status.text }}</Badge>
</template>
