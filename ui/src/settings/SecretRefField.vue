<script setup lang="ts">
import { ref } from "vue";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SecretReference } from "@/components/ui/secret-reference";

/** Where a secret lives. Archie resolves it; the value never reaches the UI. */
interface SecretRef {
  engine: string;
  key: string;
}

// resolved stays unset until something actually resolves the reference: a
// filled-in engine and key says nothing about whether the secret exists.
withDefaults(defineProps<{ resolved?: boolean; disabled?: boolean; idPrefix: string }>(), {
  resolved: undefined,
});
const model = defineModel<SecretRef>({ required: true });
const editing = ref(false);
</script>

<template>
  <div v-if="!editing">
    <SecretReference
      :engine="model.engine || 'unset'"
      :secret-key="model.key || 'unset'"
      :resolved="resolved"
      :disabled="disabled"
      @change="editing = true"
    />
  </div>
  <div v-else class="flex flex-wrap items-end gap-2">
    <label class="grid gap-1 text-xs text-fg-subtle">
      Engine
      <Input :id="`${idPrefix}-engine`" v-model="model.engine" class="w-28 font-mono" />
    </label>
    <label class="grid min-w-48 flex-1 gap-1 text-xs text-fg-subtle">
      Key
      <Input :id="`${idPrefix}-key`" v-model="model.key" class="font-mono" />
    </label>
    <Button variant="ghost" size="sm" @click="editing = false">Done</Button>
  </div>
</template>
