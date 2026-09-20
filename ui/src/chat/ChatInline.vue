<script setup lang="ts">
import type { ChatInline } from "./markdown";

/** One inline run: the spans of a paragraph, a heading or a table cell. */
defineProps<{ inline: ChatInline[] }>();
</script>

<template>
  <template v-for="(span, i) in inline" :key="i">
    <img
      v-if="span.kind === 'image'"
      :src="span.src"
      :alt="span.alt"
      loading="lazy"
      class="my-1 block max-h-96 max-w-full cursor-pointer rounded-md border border-border bg-card object-contain"
    />
    <del v-else-if="span.kind === 'del'">{{ span.text }}</del>
    <strong v-else-if="span.kind === 'strong'">{{ span.text }}</strong>
    <code
      v-else-if="span.kind === 'code'"
      class="rounded bg-foreground/10 px-1 py-0.5 font-mono text-[0.9em]"
      >{{ span.text }}</code
    >
    <a
      v-else-if="span.kind === 'link'"
      :href="span.href"
      target="_blank"
      rel="noreferrer"
      class="text-primary underline-offset-4 hover:underline"
      >{{ span.text }}</a
    >
    <em v-else-if="span.kind === 'em'">{{ span.text }}</em>
    <template v-else>{{ span.text }}</template>
  </template>
</template>
