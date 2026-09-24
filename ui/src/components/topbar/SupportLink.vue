<script setup lang="ts">
import { CircleQuestionMark } from "@lucide/vue";

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
</script>

<template>
  <Tooltip>
    <TooltipTrigger as-child>
      <a
        href="https://github.com/samcharles93/archie-core/tree/main/docs"
        target="_blank"
        rel="noreferrer noopener"
        class="text-muted-foreground hover:text-foreground hover:bg-accent focus-visible:ring-ring/50 inline-flex size-8 items-center justify-center rounded-md outline-none focus-visible:ring-[3px]"
        aria-label="Documentation"
      >
        <CircleQuestionMark :size="16" />
      </a>
    </TooltipTrigger>
    <TooltipContent :side-offset="8">Documentation</TooltipContent>
  </Tooltip>
</template>
