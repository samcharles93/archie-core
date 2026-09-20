<script setup lang="ts">
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { NavEntry } from "@/lib/nav";

defineProps<{ entry: NavEntry; current: boolean }>();
</script>

<template>
  <Tooltip>
    <TooltipTrigger as-child>
      <DropdownMenuItem
        as-child
        :data-current="current ? '' : undefined"
        :disabled="entry.soon"
        class="data-current:text-foreground data-current:bg-accent/60 px-2 py-1.5"
      >
        <RouterLink :to="entry.path" class="w-full cursor-pointer">
          <span class="flex-1">{{ entry.label }}</span>
          <span v-if="entry.soon" class="text-muted-foreground text-[11px] tracking-wide uppercase">soon</span>
        </RouterLink>
      </DropdownMenuItem>
    </TooltipTrigger>
    <TooltipContent side="right" :side-offset="10" class="max-w-64">
      {{ entry.soon ? "Coming soon. " + entry.description : entry.description }}
    </TooltipContent>
  </Tooltip>
</template>
