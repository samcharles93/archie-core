<script lang="ts">
/** One thing a curator did, and why it did it. */
export interface CuratorActionEntry {
  type?: string;
  at?: string | number;
  detail?: string;
  reason?: string;
}
</script>

<script setup lang="ts">
import { ago } from "@/lib/format";

const props = defineProps<{ action: CuratorActionEntry }>();
</script>

<template>
  <li class="rounded-sm bg-muted px-3 py-2">
    <div class="flex items-baseline justify-between gap-2">
      <span class="text-sm font-medium">{{ props.action.type || "action" }}</span>
      <span class="text-xs text-fg-subtle">{{ ago(props.action.at) }}</span>
    </div>
    <p v-if="props.action.detail" class="mt-1 text-sm text-fg-muted">{{ props.action.detail }}</p>
    <!-- The reason is the point of this list: an action without one is not
         reviewable. -->
    <p v-if="props.action.reason" class="mt-1 text-xs text-fg-subtle">Why: {{ props.action.reason }}</p>
  </li>
</template>
