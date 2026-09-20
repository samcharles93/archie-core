<script setup lang="ts">
import { storeToRefs } from 'pinia'
import type { TooltipRootEmits, TooltipRootProps } from 'reka-ui'
import { TooltipRoot, useForwardPropsEmits } from 'reka-ui'
import { useAppearanceStore } from '@/stores/appearance'

const props = defineProps<TooltipRootProps>()
const emits = defineEmits<TooltipRootEmits>()

const forwarded = useForwardPropsEmits(props, emits)
const { tooltipsEnabled } = storeToRefs(useAppearanceStore())
</script>

<template>
  <TooltipRoot
    v-slot="slotProps"
    data-slot="tooltip"
    v-bind="forwarded"
    :disabled="props.disabled || !tooltipsEnabled"
  >
    <slot v-bind="slotProps" />
  </TooltipRoot>
</template>
