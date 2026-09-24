<script setup lang="ts">
import { computed } from "vue";

import ChatInline from "./ChatInline.vue";
import { parseMarkdown } from "./markdown";

/**
 * A reply rendered as semantic HTML: headings, lists, quotes, fenced code,
 * tables, inline emphasis. The parse is a computed, so a reply that grows a
 * token at a time re-renders from the text it now has rather than accumulating
 * markup.
 */
const props = defineProps<{ text: string }>();

const blocks = computed(() => parseMarkdown(props.text));
</script>

<template>
  <div class="[overflow-wrap:anywhere]">
    <template v-for="(block, i) in blocks" :key="i">
      <pre
        v-if="block.kind === 'pre'"
        class="my-2 overflow-auto rounded-md border border-border bg-background/60 p-3"
      ><code class="font-mono text-[0.85em]">{{ block.code }}</code></pre>

      <table
        v-else-if="block.kind === 'table'"
        class="my-3 w-full border-collapse text-[0.92em]"
      >
        <thead>
          <tr>
            <th
              v-for="(cell, c) in block.head"
              :key="c"
              class="border border-border bg-muted px-2 py-1.5 text-left font-semibold"
            >
              <ChatInline :inline="cell" />
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, r) in block.rows" :key="r">
            <td
              v-for="(cell, c) in row"
              :key="c"
              class="border border-border px-2 py-1.5 text-left"
            >
              <ChatInline :inline="cell" />
            </td>
          </tr>
        </tbody>
      </table>

      <blockquote
        v-else-if="block.kind === 'blockquote'"
        class="my-2 border-l-[3px] border-primary pl-3 text-fg-muted"
      >
        <ChatInline :inline="block.inline" />
      </blockquote>

      <h1
        v-else-if="block.kind === 'h1'"
        class="mt-1 mb-2 text-xl leading-tight font-semibold tracking-[-0.02em]"
      >
        <ChatInline :inline="block.inline" />
      </h1>
      <h2
        v-else-if="block.kind === 'h2'"
        class="mt-1 mb-2 text-lg leading-tight font-semibold tracking-[-0.02em]"
      >
        <ChatInline :inline="block.inline" />
      </h2>
      <h3
        v-else-if="block.kind === 'h3'"
        class="mt-1 mb-2 text-base leading-tight font-semibold tracking-[-0.02em]"
      >
        <ChatInline :inline="block.inline" />
      </h3>

      <ul
        v-else-if="block.kind === 'ul'"
        class="my-2.5 flex list-disc flex-col gap-1 pl-5"
      >
        <li v-for="(item, n) in block.items" :key="n">
          <ChatInline :inline="item" />
        </li>
      </ul>
      <ol
        v-else-if="block.kind === 'ol'"
        class="my-2.5 flex list-decimal flex-col gap-1 pl-5"
      >
        <li v-for="(item, n) in block.items" :key="n">
          <ChatInline :inline="item" />
        </li>
      </ol>

      <p v-else-if="block.kind === 'p'" class="mb-2.5 last:mb-0">
        <ChatInline :inline="block.inline" />
      </p>
    </template>
  </div>
</template>
