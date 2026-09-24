<script setup lang="ts">
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import type { NavEntry } from "@/lib/nav";

defineProps<{ entry: NavEntry; current: boolean }>();
</script>

<template>
  <DropdownMenuItem
    as-child
    :data-current="current ? '' : undefined"
    :disabled="entry.soon"
    class="data-current:text-foreground data-current:bg-accent/60 px-2 py-1.5"
  >
    <RouterLink :to="entry.path" class="w-full cursor-pointer">
      <span class="flex-1">{{ entry.label }}</span>
      <span
        v-if="entry.soon"
        class="text-muted-foreground text-[11px] tracking-wide uppercase"
        >soon</span
      >
    </RouterLink>
  </DropdownMenuItem>
</template>
