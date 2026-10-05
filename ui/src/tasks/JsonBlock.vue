<script setup lang="ts">
/**
 * A JSON payload, verbatim. Two panels show one (the attempt's recorded config
 * and the debug view), and both must show exactly what the daemon stored --
 * no projection, no prettifying beyond indentation.
 */
import { computed, ref, watch } from "vue";
import { Check, Copy, TriangleAlert } from "@lucide/vue";
import HighlightedJson from "@/base/HighlightedJson.vue";

const props = defineProps<{ value: unknown }>();
const text = computed(() => JSON.stringify(props.value ?? {}, null, 2));
const copied = ref(false);
const copyError = ref(false);
watch(text, () => { copied.value = false; copyError.value = false; });
async function copy(): Promise<void> {
  copyError.value = false;
  try {
    await navigator.clipboard.writeText(text.value);
    copied.value = true;
  } catch {
    copied.value = false;
    copyError.value = true;
  }
}
</script>

<template>
  <div class="group/json relative">
    <button
      type="button"
      class="absolute right-2 top-2 z-10 flex size-7 items-center justify-center rounded-md bg-muted text-muted-foreground opacity-0 transition-opacity hover:bg-secondary hover:text-foreground focus-visible:opacity-100 focus-visible:outline-2 focus-visible:outline-ring group-hover/json:opacity-100 group-focus-within/json:opacity-100"
      :aria-label="copyError ? 'Copy failed. Retry copying output' : copied ? 'Copied output' : 'Copy output'"
      :title="copyError ? 'Could not copy output. Click to retry.' : copied ? 'Copied' : 'Copy output'"
      @click="copy"
    >
      <TriangleAlert v-if="copyError" class="size-4 text-danger" />
      <Check v-else-if="copied" class="size-4" />
      <Copy v-else class="size-4" />
    </button>
    <span class="sr-only" role="status">{{ copyError ? 'Could not copy output.' : copied ? 'Output copied.' : '' }}</span>
    <pre
      class="max-h-[60vh] overflow-auto rounded-sm border border-border bg-muted p-3 font-mono text-xs leading-normal whitespace-pre-wrap break-words"
    ><HighlightedJson :text="text" /></pre>
  </div>
</template>
