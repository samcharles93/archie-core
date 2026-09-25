<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
import type { DurationUnit } from "./duration";
import {
  DURATION_UNITS,
  formatGoDuration,
  joinDuration,
  parseGoDuration,
  splitDuration,
} from "./duration";

const props = defineProps<{ id?: string; disabled?: boolean; units?: DurationUnit[] }>();
const model = defineModel<string>({ required: true });

const units = computed(() => props.units ?? DURATION_UNITS);
const amount = ref(0);
const unit = ref<DurationUnit>("s");

watch(
  model,
  (text) => {
    const ms = parseGoDuration(text ?? "");
    if (ms === null) return;
    if (joinDuration(amount.value, unit.value) === ms) return;
    const split = splitDuration(ms);
    amount.value = split.value;
    unit.value = units.value.includes(split.unit) ? split.unit : "ms";
  },
  { immediate: true },
);

function emitChange() {
  if (!Number.isFinite(amount.value) || amount.value < 0) return;
  model.value = formatGoDuration(joinDuration(amount.value, unit.value));
}
</script>

<template>
  <InputGroup class="w-48">
    <InputGroupInput
      :id="id"
      v-model.number="amount"
      type="number"
      min="0"
      inputmode="decimal"
      class="font-mono"
      :disabled="disabled"
      @change="emitChange"
    />
    <InputGroupAddon align="inline-end">
      <select
        v-model="unit"
        aria-label="Unit"
        class="bg-transparent font-mono text-xs text-muted-foreground outline-none"
        :disabled="disabled"
        @change="emitChange"
      >
        <option v-for="u in units" :key="u" :value="u">{{ u }}</option>
      </select>
    </InputGroupAddon>
  </InputGroup>
</template>
