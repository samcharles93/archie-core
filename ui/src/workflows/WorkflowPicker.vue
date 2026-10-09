<script setup lang="ts">
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

defineProps<{ value: string; workflows: { id: string }[]; label: string }>();
const emit = defineEmits<{ pick: [string] }>();
</script>

<template>
  <Select :model-value="value || undefined" @update:model-value="emit('pick', String($event))">
    <SelectTrigger :aria-label="label"><SelectValue placeholder="Choose workflow" /></SelectTrigger>
    <SelectContent>
      <SelectItem v-if="value && !workflows.some((entry) => entry.id === value)" :value="value" disabled>{{ value }} (unavailable)</SelectItem>
      <SelectItem v-for="entry in workflows" :key="entry.id" :value="entry.id">{{ entry.id }}</SelectItem>
    </SelectContent>
  </Select>
</template>
