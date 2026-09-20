<script setup lang="ts">
import { computed } from "vue";
import { Moon, Sun } from "@lucide/vue";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { theme, toggleTheme } from "@/lib/theme";

const label = computed(() => (theme.value === "dark" ? "Switch to light" : "Switch to dark"));
</script>

<template>
  <Tooltip>
    <TooltipTrigger as-child>
      <button
        type="button"
        class="text-muted-foreground hover:text-foreground hover:bg-accent focus-visible:ring-ring/50 inline-flex size-8 items-center justify-center rounded-md outline-none focus-visible:ring-[3px]"
        :aria-label="label"
        @click="toggleTheme()"
      >
        <component :is="theme === 'dark' ? Moon : Sun" :size="16" />
      </button>
    </TooltipTrigger>
    <TooltipContent :side-offset="8">{{ label }}</TooltipContent>
  </Tooltip>
</template>
