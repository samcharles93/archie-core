<script setup lang="ts">
import HistoryLink from "@/settings/HistoryLink.vue";
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { DurationInput, parseGoDuration } from "@/components/ui/duration-input";
import { Input } from "@/components/ui/input";
import {
  NumberField,
  NumberFieldContent,
  NumberFieldDecrement,
  NumberFieldIncrement,
  NumberFieldInput,
} from "@/components/ui/number-field";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { SettingRow } from "@/components/ui/setting-row";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";

const KIND = "scheduling-policy";

interface SchedulingPolicy {
  poll_interval: string;
  max_retries: number;
  label?: string;
  dispatch: { trigger: string; ack_reaction: string; labels: Record<string, string> };
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "scheduling"));
const policy = computed(() => store.drafts[KIND]?.value as SchedulingPolicy | undefined);
const error = computed(() => store.stateFor(KIND).error);

const triggers = [
  { value: "assignee", label: "Assignee" },
  { value: "label", label: "Label" },
  { value: "either", label: "Either" },
];

// The states the daemon labels issues with, in lifecycle order. Each swatch
// uses the status colour the rest of the dashboard gives that state.
const states = [
  { key: "queued", name: "Queued", swatch: "bg-idle" },
  { key: "working", name: "Working", swatch: "bg-info" },
  { key: "waiting", name: "Waiting", swatch: "bg-warn" },
  { key: "pr", name: "PR open", swatch: "bg-ok" },
  { key: "parked", name: "Parked", swatch: "bg-fg-subtle" },
  { key: "dead", name: "Dead", swatch: "bg-danger" },
];

const intervalInvalid = computed(() => {
  const ms = parseGoDuration(policy.value?.poll_interval ?? "");
  return ms === null || ms <= 0;
});
const labelMissing = computed(
  () => policy.value !== undefined && policy.value.dispatch.trigger !== "assignee" && !policy.value.label,
);
</script>

<template>
  <div>
    <PageHeader title="Scheduling policy">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">
      {{ catalogError || error }}
    </p>

    <template v-if="policy">
      <h2 class="mb-1 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase">Dispatch</h2>
      <SettingRow label="Trigger">
        <SegmentedControl v-model="policy.dispatch.trigger" label="Trigger" :options="triggers" />
      </SettingRow>
      <SettingRow
        v-if="policy.dispatch.trigger !== 'assignee'"
        label="Label"
        for="sp-label"
      >
        <Input
          id="sp-label"
          :model-value="policy.label ?? ''"
          class="max-w-sm font-mono"
          :aria-invalid="labelMissing || undefined"
          @update:model-value="policy.label = String($event)"
        />
        <p v-if="labelMissing" class="mt-1.5 text-xs text-danger">
          Required: an empty label matches every open issue.
        </p>
      </SettingRow>
      <SettingRow label="Ack reaction" for="sp-ack" hint="Empty: none.">
        <Input id="sp-ack" v-model="policy.dispatch.ack_reaction" class="max-w-xs font-mono" />
      </SettingRow>

      <h2 class="mt-10 mb-1 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase">State labels</h2>
      <SettingRow label="Forge labels">
        <table class="w-full max-w-lg text-sm">
          <tbody>
            <tr v-for="state in states" :key="state.key" class="border-b border-border last:border-0">
              <td class="w-36 py-1.5 pr-3">
                <span class="inline-flex items-center gap-2">
                  <span class="size-2 rounded-full" :class="state.swatch" aria-hidden="true" />
                  <label :for="`sp-state-${state.key}`">{{ state.name }}</label>
                </span>
              </td>
              <td class="py-1.5">
                <Input
                  :id="`sp-state-${state.key}`"
                  :model-value="policy.dispatch.labels[state.key] ?? ''"
                  class="font-mono"
                  @update:model-value="policy.dispatch.labels[state.key] = String($event)"
                />
              </td>
            </tr>
          </tbody>
        </table>
      </SettingRow>

      <h2 class="mt-10 mb-1 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase">Retries and polling</h2>
      <SettingRow label="Max retries" hint="Then marked dead.">
        <NumberField v-model="policy.max_retries" :min="0" class="w-32">
          <NumberFieldContent>
            <NumberFieldDecrement />
            <NumberFieldInput class="font-mono" aria-label="Max retries" />
            <NumberFieldIncrement />
          </NumberFieldContent>
        </NumberField>
      </SettingRow>
      <SettingRow label="Poll interval" for="sp-poll">
        <DurationInput id="sp-poll" v-model="policy.poll_interval" :units="['s', 'm', 'h']" />
        <p v-if="intervalInvalid" class="mt-1.5 text-xs text-danger">Must be longer than zero.</p>
      </SettingRow>
    </template>

  </div>
</template>
