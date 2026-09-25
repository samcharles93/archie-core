<script setup lang="ts">
import { CircleCheck, CircleAlert, Lock } from "@lucide/vue";
import { Button } from "@/components/ui/button";

// resolved defaults to undefined, not false: Vue casts an omitted boolean prop
// to false, which would claim "not found" for a reference nothing checked.
withDefaults(
  defineProps<{
    engine: string;
    secretKey: string;
    /** undefined while unknown; the status line is hidden. */
    resolved?: boolean;
    disabled?: boolean;
  }>(),
  { resolved: undefined },
);
defineEmits<{ change: [] }>();
</script>

<template>
  <div class="flex flex-col gap-1.5">
    <div class="flex h-9 items-center gap-2 rounded-md border border-input bg-background px-3 font-mono text-[13px]">
      <Lock class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span class="text-muted-foreground">{{ engine }}</span>
      <span class="text-muted-foreground" aria-hidden="true">/</span>
      <span class="min-w-0 flex-1 truncate">{{ secretKey }}</span>
      <Button variant="ghost" size="sm" class="h-7" :disabled="disabled" @click="$emit('change')">
        Change
      </Button>
    </div>
    <p v-if="resolved === true" class="flex items-center gap-1.5 text-xs text-ok">
      <CircleCheck class="size-3.5" aria-hidden="true" /> Credential resolved
    </p>
    <p v-else-if="resolved === false" class="flex items-center gap-1.5 text-xs text-danger">
      <CircleAlert class="size-3.5" aria-hidden="true" /> Credential not found
    </p>
  </div>
</template>
