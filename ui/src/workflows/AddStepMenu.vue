<script setup lang="ts">
import { Plus } from "@lucide/vue";

import { DropdownMenu, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import StepTypeItems from "./StepTypeItems.vue";

defineProps<{ types: string[]; label?: string }>();
const emit = defineEmits<{ pick: [string] }>();
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <button
        type="button"
        class="nodrag nopan flex size-6 items-center justify-center rounded-full border border-border bg-card text-muted-foreground shadow-sm transition-colors hover:border-primary hover:text-primary"
        :aria-label="label ?? 'Add a step here'"
        :title="label ?? 'Add a step here'"
      >
        <Plus class="size-3.5" />
      </button>
    </DropdownMenuTrigger>
    <DropdownMenuContent class="w-52" align="center">
      <DropdownMenuLabel>{{ label ?? "Add a step here" }}</DropdownMenuLabel>
      <StepTypeItems :types="types" @pick="emit('pick', $event)" />
    </DropdownMenuContent>
  </DropdownMenu>
</template>
