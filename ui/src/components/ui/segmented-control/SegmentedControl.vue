<script setup lang="ts" generic="T extends string">
import { RadioGroupItem, RadioGroupRoot } from "reka-ui";
import { cn } from "@/lib/utils";

defineProps<{
  options: { value: T; label: string }[];
  label: string;
  disabled?: boolean;
  class?: string;
}>();
const model = defineModel<T>({ required: true });
</script>

<template>
  <RadioGroupRoot
    v-model="model"
    :aria-label="label"
    :disabled="disabled"
    orientation="horizontal"
    loop
    :class="cn('inline-flex h-9 items-center gap-0.5 rounded-md border border-input bg-background p-0.5', $props.class)"
  >
    <RadioGroupItem
      v-for="option in options"
      :key="option.value"
      :value="option.value"
      class="h-full rounded-sm px-3 text-[13px] font-medium text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring data-[state=checked]:bg-secondary data-[state=checked]:text-foreground disabled:opacity-50"
    >
      {{ option.label }}
    </RadioGroupItem>
  </RadioGroupRoot>
</template>
