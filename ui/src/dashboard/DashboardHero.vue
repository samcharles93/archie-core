<script setup lang="ts">
import { computed } from "vue";
import { AlignLeft } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { attentionStatusIds } from "@/lib/task-meta";
import { headline } from "./headline";
import { error, summary } from "./state";

const text = computed(() => {
  const counts = summary.value?.statuses ?? {};
  const ids = attentionStatusIds();
  const attention = Object.entries(counts).reduce((sum, [s, n]) => (ids.has(s) ? sum + (n || 0) : sum), 0);
  return { ...headline(counts.running || 0, attention), urgent: attention > 0 };
});
</script>

<template>
  <div class="mb-6 flex flex-wrap items-center justify-between gap-4">
    <div class="min-w-0">
      <h1 v-if="summary" class="text-2xl font-semibold">
        {{ text.running }}<span class="text-fg-subtle"> · </span
        ><span :class="text.urgent && 'text-warn'">{{ text.attention }}</span>
      </h1>
      <h1 v-else class="text-2xl font-semibold">Dashboard</h1>
      <p v-if="error" class="mt-1 text-sm text-danger" role="alert">Cannot reach archied: {{ error }}</p>
    </div>
    <Button variant="outline" size="sm" as-child>
      <RouterLink to="/logs"><AlignLeft data-icon="inline-start" /> Logs</RouterLink>
    </Button>
  </div>
</template>
