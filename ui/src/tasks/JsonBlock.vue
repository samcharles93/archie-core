<script setup lang="ts">
/**
 * A JSON payload, verbatim. Two panels show one (the attempt's recorded config
 * and the debug view), and both must show exactly what the daemon stored --
 * no projection, no prettifying beyond indentation.
 */
import { computed, ref, watch } from "vue";
import { Check, Copy } from "@lucide/vue";
import { Button } from "@/components/ui/button";
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
  <div class="mb-2 flex items-center justify-end gap-2">
    <p v-if="copyError" role="alert" class="text-xs text-danger">Could not copy output.</p>
    <Button type="button" variant="outline" size="sm" @click="copy"><Check v-if="copied" /><Copy v-else />{{ copied ? "Copied" : "Copy output" }}</Button>
  </div>
  <!--
    pre-wrap keeps the indentation the JSON is printed with while letting a
    long value wrap instead of forcing the panel to scroll sideways.
  -->
  <pre
    class="max-h-[60vh] overflow-auto rounded-sm border border-border bg-muted p-3 font-mono text-xs leading-normal whitespace-pre-wrap break-words"
  ><HighlightedJson :text="text" /></pre>
</template>
