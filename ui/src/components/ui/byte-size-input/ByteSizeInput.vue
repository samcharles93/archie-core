<script setup lang="ts">
import { ref, watch } from "vue";
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from "@/components/ui/input-group";
import { bytesToMB, mbToBytes } from "./bytes";

defineProps<{ id?: string; disabled?: boolean }>();
const model = defineModel<number>({ required: true });

const mb = ref(0);
watch(
  model,
  (bytes) => {
    if (mbToBytes(mb.value) !== bytes) mb.value = bytesToMB(bytes ?? 0);
  },
  { immediate: true },
);

function emitChange() {
  if (Number.isFinite(mb.value) && mb.value >= 0) model.value = mbToBytes(mb.value);
}
</script>

<template>
  <InputGroup class="w-40">
    <InputGroupInput
      :id="id"
      v-model.number="mb"
      type="number"
      min="0"
      step="any"
      inputmode="decimal"
      class="font-mono"
      :disabled="disabled"
      @change="emitChange"
    />
    <InputGroupAddon align="inline-end">
      <InputGroupText class="font-mono text-xs">MB</InputGroupText>
    </InputGroupAddon>
  </InputGroup>
</template>
