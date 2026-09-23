<script setup lang="ts">
/**
 * A JSON payload, verbatim. Two panels show one (the attempt's recorded config
 * and the debug view), and both must show exactly what the daemon stored --
 * no projection, no prettifying beyond indentation.
 */
import HighlightedJson from "@/base/HighlightedJson.vue";

defineProps<{ value: unknown }>();
</script>

<template>
  <!--
    pre-wrap keeps the indentation the JSON is printed with while letting a
    long value wrap instead of forcing the panel to scroll sideways.
  -->
  <pre
    class="max-h-[60vh] overflow-auto rounded-sm border border-border bg-muted p-3 font-mono text-xs leading-normal whitespace-pre-wrap break-words"
  ><HighlightedJson :text="JSON.stringify(value ?? {}, null, 2)" /></pre>
</template>
