<script setup lang="ts">
import { Handle, Position } from "@vue-flow/core";

import AddStepMenu from "./AddStepMenu.vue";

defineProps<{ data: { branch?: string; types: string[]; editable: boolean; onInsert: (type: string) => void } }>();
</script>

<template>
  <div class="relative flex w-8 justify-center">
    <Handle type="target" :position="Position.Top" class="!opacity-0" />
    <AddStepMenu v-if="data.editable" :types="data.types" :label="data.branch ? `Add a step to ${data.branch}` : 'Add a step at the end'" @pick="data.onInsert" />
    <Handle v-if="data.branch" type="source" :position="Position.Bottom" class="!opacity-0" />
    <span v-else class="size-2 rounded-full bg-border" aria-hidden="true" />
  </div>
</template>
