<script setup lang="ts">
import { Handle, Position } from "@vue-flow/core";

import AddStepMenu from "./AddStepMenu.vue";

defineProps<{ data: { types: string[]; editable: boolean; onInsert: (type: string) => void } }>();
</script>

<template>
  <div class="flex w-8 justify-center">
    <Handle type="target" :position="Position.Top" class="!opacity-0" />
    <AddStepMenu v-if="data.editable" :types="data.types" label="Add a step at the end" @pick="data.onInsert" />
    <span v-else class="size-2 rounded-full bg-border" aria-hidden="true" />
  </div>
</template>
