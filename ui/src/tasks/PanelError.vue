<script setup lang="ts">
import { Button } from "@/components/ui/button";

/**
 * A panel whose read failed. A failed read is reported as a failed read: it is
 * never dressed up as an empty result, which would be a claim about the run
 * that nothing here can make.
 */
defineProps<{ title: string; detail: string }>();
defineEmits<{ retry: [] }>();
</script>

<template>
  <div class="flex flex-col items-start gap-2 py-3">
    <p class="font-medium">{{ title }}</p>
    <p class="max-w-[60ch] text-sm text-fg-muted">{{ detail }}</p>
    <Button variant="outline" @click="$emit('retry')">Retry</Button>
  </div>
</template>
