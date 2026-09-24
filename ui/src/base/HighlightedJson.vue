<script setup lang="ts">
/**
 * Inline JSON content with syntax highlighting, or the text verbatim when it
 * is not JSON. Every source character survives either way — highlighting adds
 * colour spans, never removes or rewrites text. Callers own the surrounding
 * <pre> and its classes (borders, max height, wrap behaviour differ per
 * surface); this renders only the content.
 */
import { computed } from "vue";

import { tokenClass, tokenizeJson } from "@/lib/json-tokens";

const props = defineProps<{ text: string }>();

const tokens = computed(() => tokenizeJson(props.text));
</script>

<template>
  <template v-if="tokens">
    <span
      v-for="(token, i) in tokens"
      :key="i"
      :class="tokenClass(token.kind)"
      >{{ token.text }}</span
    >
  </template>
  <template v-else>{{ props.text }}</template>
</template>
