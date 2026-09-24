<script setup lang="ts">
import { computed } from "vue";

import HighlightedJson from "@/base/HighlightedJson.vue";
import { prettyPrint } from "./capture-payload";

/**
 * One labelled block of a capture: its body or its headers, as text. Both are
 * stored as strings, valid JSON or not, so both go through the same reader.
 */
const props = defineProps<{ label: string; raw?: string }>();

const text = computed(() => prettyPrint(props.raw));
</script>

<template>
  <section class="min-w-0">
    <h3
      class="mb-2 text-xs font-semibold tracking-[0.05em] text-fg-subtle uppercase"
    >
      {{ label }}
    </h3>
    <!--
      Wrap rather than scroll sideways: a captured payload is mostly long
      unbroken tokens (URLs, hashes, base64), and one of those on a single line
      would push the panel's contents past its edge instead of being read.
    -->
    <p v-if="!text" class="text-sm text-fg-muted">(empty)</p>
    <pre
      v-else
      class="rounded-sm border border-border-strong bg-muted p-3 font-mono text-xs whitespace-pre-wrap wrap-break-word"
    ><HighlightedJson :text="text" /></pre>
  </section>
</template>
