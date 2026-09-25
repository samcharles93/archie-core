<script setup lang="ts">
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const pill = cva(
  "inline-flex h-6 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border px-2.5 text-xs font-medium",
  {
    variants: {
      tone: {
        neutral: "border-border bg-secondary text-muted-foreground",
        accent: "border-primary/50 bg-transparent text-primary",
        warn: "border-warn/40 bg-warn-soft text-warn",
        danger: "border-danger/40 bg-danger-soft text-danger",
      },
      dot: {
        none: "",
        live: "before:size-1.5 before:rounded-full before:bg-primary",
        warn: "before:size-1.5 before:rounded-full before:bg-warn",
        danger: "before:size-1.5 before:rounded-full before:bg-danger",
        idle: "before:size-1.5 before:rounded-full before:bg-idle",
      },
    },
    defaultVariants: { tone: "neutral", dot: "none" },
  },
);

type PillVariants = VariantProps<typeof pill>;
defineProps<{ tone?: PillVariants["tone"]; dot?: PillVariants["dot"]; class?: string }>();
</script>

<template>
  <span :class="cn(pill({ tone, dot }), $props.class)"><slot /></span>
</template>
