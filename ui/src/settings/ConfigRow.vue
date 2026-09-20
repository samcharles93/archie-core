<script setup lang="ts">
import { computed } from "vue";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { valueText } from "./config-field";
import type { ConfigField } from "./types";

/**
 * One row of a key/value list: the label and the value the process is running
 * with. Configuration is written through the control plane, so this list
 * reports what is in effect and never offers a control of its own.
 *
 * A locked field shows the reason it stays bootstrap-owned: those keys are the
 * daemon's own startup inputs and the control plane deliberately does not
 * manage them.
 *
 * Every row is a subgrid of the list's three tracks, so the columns line up
 * across rows; see ConfigList.
 */
const props = defineProps<{
  label: string;
  value?: unknown;
  /** The descriptor carrying the row's description and locked reason. */
  field?: ConfigField;
}>();

const text = computed(() => valueText(props.value));
const lockedReason = computed(() => props.field?.locked_reason ?? "");
</script>

<template>
  <div
    class="grid grid-cols-1 items-center gap-x-4 gap-y-1 border-t border-border py-2 first:border-t-0 hover:bg-muted/40 min-[701px]:grid-cols-[minmax(8rem,16rem)_minmax(0,1fr)_auto] min-[701px]:supports-[grid-template-columns:subgrid]:col-span-full min-[701px]:supports-[grid-template-columns:subgrid]:grid-cols-subgrid"
  >
    <!-- The field's own description is the row's tooltip: the line that
         explains a setting is the one place that explanation fits without
         pushing every row apart. -->
    <Tooltip v-if="field?.description">
      <TooltipTrigger as-child>
        <!--
          tabindex="0" is the keyboard path to the description: reka wires the
          trigger's focus/blur to the tooltip's open state, so Tab reaches the
          label and holding focus keeps the description on screen. The
          aria-label carries the label and the description together, because
          the tooltip content itself is out of the accessibility tree when the
          trigger is not hovered or focused.
        -->
        <span tabindex="0" :aria-label="`${label}: ${field.description}`" class="text-sm text-fg-muted">{{ label }}</span>
      </TooltipTrigger>
      <TooltipContent :side-offset="8" class="max-w-80">{{ field.description }}</TooltipContent>
    </Tooltip>
    <span v-else class="text-sm text-fg-muted">{{ label }}</span>

    <!-- min-w-0 is load-bearing: a grid item refuses to shrink below its
         content by default, which is what pushed long paths into the action
         column and truncated them against the buttons. -->
    <slot name="value">
      <span
        :class="cn('min-w-0 break-words font-mono text-sm min-[701px]:truncate', text === '—' ? 'text-fg-subtle' : 'text-foreground', lockedReason && 'text-fg-muted')"
        :title="text"
      >{{ text }}</span>
    </slot>

    <span v-if="lockedReason" class="col-span-full text-xs text-fg-subtle min-[701px]:col-span-2 min-[701px]:col-start-2">{{ lockedReason }}</span>
  </div>
</template>
