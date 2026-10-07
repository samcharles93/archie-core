<script setup lang="ts">
import { computed } from "vue";

import { Button } from "@/components/ui/button";
import { selectorData, setModel, setPersona } from "./state";

const props = defineProps<{ choose: "model" | "personality" }>();

const options = computed(() =>
  props.choose === "model" ? (selectorData.value.models ?? []) : (selectorData.value.personas ?? []),
);
const active = computed(() =>
  props.choose === "model" ? selectorData.value.active_model : selectorData.value.active_persona,
);

function pick(value: string) {
  if (props.choose === "model") void setModel(value);
  else setPersona(value);
}
</script>

<template>
  <div class="text-sm">
    <p class="font-medium">{{ choose === "model" ? "Choose a model" : "Choose a personality" }}</p>
    <p v-if="!options.length" class="mt-1 text-fg-muted">None configured.</p>
    <div v-else class="mt-2 flex flex-wrap gap-2">
      <Button
        v-for="option in options"
        :key="option"
        size="sm"
        :variant="option === active ? 'default' : 'outline'"
        class="font-mono"
        @click="pick(option)"
        >{{ option }}</Button
      >
    </div>
  </div>
</template>
