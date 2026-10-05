<script setup lang="ts">
import { computed, ref } from "vue";
import { Check, Copy, KeyRound, X } from "@lucide/vue";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { sourceURL, type Source } from "./source-signing";

const props = defineProps<{ source: Source }>();
const emit = defineEmits<{ dismiss: [] }>();
const url = computed(() => sourceURL(window.location.origin, props.source.path));
const copied = ref<string | null>(null);
async function copy(value: string): Promise<void> {
  await navigator.clipboard.writeText(value);
  copied.value = value;
  setTimeout(() => (copied.value = null), 1500);
}
</script>

<template>
  <Alert>
    <KeyRound />
    <AlertTitle class="flex items-center justify-between gap-2 text-xs">
      <span class="font-mono"
        >{{ source.path }} · signing secret, shown once</span
      >
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label="Dismiss"
        @click="emit('dismiss')"
        ><X
      /></Button>
    </AlertTitle>
    <AlertDescription class="col-start-2 flex items-center gap-2">
      <code class="min-w-0 flex-1 break-all font-mono text-xs">{{ url }}</code>
      <Button type="button" variant="ghost" size="icon-sm" aria-label="Copy URL" @click="copy(url)"><Check v-if="copied === url" /><Copy v-else /></Button>
    </AlertDescription>
    <AlertDescription class="col-start-2 flex items-center gap-2">
      <code class="min-w-0 flex-1 break-all font-mono text-xs">{{
        source.secret
      }}</code>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label="Copy secret"
        @click="copy(source.secret || '')"
      >
        <Check v-if="copied === source.secret" />
        <Copy v-else />
      </Button>
    </AlertDescription>
  </Alert>
</template>
