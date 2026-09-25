<script setup lang="ts">
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { DurationInput, formatGoDuration, parseGoDuration } from "@/components/ui/duration-input";
import {
  NumberField,
  NumberFieldContent,
  NumberFieldDecrement,
  NumberFieldIncrement,
  NumberFieldInput,
} from "@/components/ui/number-field";
import { SettingRow } from "@/components/ui/setting-row";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import HistoryLink from "./HistoryLink.vue";

const KIND = "workflow-execution-settings";

interface ExecutionSettings {
  max_model_tool_steps: number;
  max_runtime_seconds: number;
  max_consecutive_gate_failures: number;
  max_task_runtime_seconds: number | null;
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "tasks"));
const limits = computed(() => store.drafts[KIND]?.value as ExecutionSettings | undefined);
const error = computed(() => store.stateFor(KIND).error);

// The document counts seconds; the input speaks durations.
function seconds(key: "max_runtime_seconds" | "max_task_runtime_seconds") {
  return computed({
    get: () => formatGoDuration((limits.value?.[key] ?? 0) * 1000),
    set: (text: string) => {
      const ms = parseGoDuration(text);
      if (limits.value && ms !== null) limits.value[key] = Math.round(ms / 1000);
    },
  });
}
const callRuntime = seconds("max_runtime_seconds");
const taskRuntime = seconds("max_task_runtime_seconds");
</script>

<template>
  <div>
    <PageHeader title="Task execution">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>
    <p class="-mt-4 mb-8 text-sm text-fg-muted">Limits every task runs under. Zero turns a limit off.</p>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">
      {{ catalogError || error }}
    </p>

    <template v-if="limits">
      <SettingRow label="Task time limit" for="te-task" hint="A task still running after this long is parked.">
        <DurationInput id="te-task" v-model="taskRuntime" :units="['m', 'h']" />
      </SettingRow>
      <SettingRow label="Agent call time limit" for="te-call" hint="The longest one agent call inside a stage may run.">
        <DurationInput id="te-call" v-model="callRuntime" :units="['s', 'm', 'h']" />
      </SettingRow>
      <SettingRow label="Model tool steps" hint="Model and tool round-trips one agent call may take.">
        <NumberField v-model="limits.max_model_tool_steps" :min="0" class="w-32">
          <NumberFieldContent>
            <NumberFieldDecrement />
            <NumberFieldInput class="font-mono" aria-label="Model tool steps" />
            <NumberFieldIncrement />
          </NumberFieldContent>
        </NumberField>
      </SettingRow>
      <SettingRow label="Gate failures in a row" hint="Consecutive failed quality gates before the task is parked.">
        <NumberField v-model="limits.max_consecutive_gate_failures" :min="0" class="w-32">
          <NumberFieldContent>
            <NumberFieldDecrement />
            <NumberFieldInput class="font-mono" aria-label="Gate failures in a row" />
            <NumberFieldIncrement />
          </NumberFieldContent>
        </NumberField>
      </SettingRow>
    </template>
  </div>
</template>
