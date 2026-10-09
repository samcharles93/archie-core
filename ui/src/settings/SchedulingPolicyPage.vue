<script setup lang="ts">
import ApplyStatusRows from "./ApplyStatusRows.vue";
import HistoryLink from "@/settings/HistoryLink.vue";
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { DurationInput } from "@/components/ui/duration-input";
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
import DraftHint from "./DraftHint.vue";

const KIND = "scheduling-policy";

interface SchedulingPolicy {
  poll_interval: string;
  max_retries: number;
  label?: string;
  dispatch: { trigger: string; ack_reaction: string };
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "scheduling"));
const policy = computed(() => store.drafts[KIND]?.value as SchedulingPolicy | undefined);
const error = computed(() => store.pageErrorFor(KIND));

const triggers = [
  { value: "assignee", label: "Assignee" },
  { value: "label", label: "Label" },
  { value: "either", label: "Either" },
];

const issues = computed(() => store.issuesFor(KIND));
const issue = (path: string) => issues.value.find((entry) => entry.path === path);
const intervalIssue = computed(() => issue("poll_interval"));
const labelIssue = computed(() => issue("label"));
</script>

<template>
  <div>
    <PageHeader title="Scheduling policy">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>
    <ApplyStatusRows v-for="resource in resources" :key="resource.kind" :kind="resource.kind" class="mb-4" />

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">
      {{ catalogError || error }}
    </p>

    <template v-if="policy">
      <h2 class="mb-1 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase">Dispatch</h2>
      <SettingRow label="Trigger">
        <div class="flex flex-wrap items-center gap-3">
          <SegmentedControl v-model="policy.dispatch.trigger" label="Trigger" :options="triggers" />
          <DraftHint :kind="KIND" path="dispatch.trigger" />
        </div>
      </SettingRow>
      <SettingRow
        v-if="policy.dispatch.trigger !== 'assignee'"
        label="Label"
        for="sp-label"
      >
        <div class="flex flex-wrap items-center gap-3">
          <Input
            id="sp-label"
            :model-value="policy.label ?? ''"
            class="max-w-sm font-mono"
            :aria-invalid="labelIssue ? true : undefined"
            @update:model-value="policy.label = String($event)"
          />
          <DraftHint :kind="KIND" path="label" />
        </div>
        <p v-if="labelIssue" class="mt-1.5 text-xs text-danger">
          {{ labelIssue.message }}
        </p>
      </SettingRow>
      <SettingRow label="Ack reaction" for="sp-ack" hint="Empty: none.">
        <div class="flex flex-wrap items-center gap-3">
          <Input id="sp-ack" v-model="policy.dispatch.ack_reaction" class="max-w-xs font-mono" />
          <DraftHint :kind="KIND" path="dispatch.ack_reaction" />
        </div>
      </SettingRow>

      <h2 class="mt-10 mb-1 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase">Retries and polling</h2>
      <SettingRow label="Max retries" hint="Then marked dead.">
        <div class="flex flex-wrap items-center gap-3">
          <NumberField v-model="policy.max_retries" :min="0" class="w-32">
            <NumberFieldContent>
              <NumberFieldDecrement />
              <NumberFieldInput class="font-mono" aria-label="Max retries" />
              <NumberFieldIncrement />
            </NumberFieldContent>
          </NumberField>
          <DraftHint :kind="KIND" path="max_retries" />
        </div>
      </SettingRow>
      <SettingRow label="Poll interval" for="sp-poll">
        <div class="flex flex-wrap items-center gap-3">
          <DurationInput id="sp-poll" v-model="policy.poll_interval" :units="['s', 'm', 'h']" />
          <DraftHint :kind="KIND" path="poll_interval" />
        </div>
        <p v-if="intervalIssue" class="mt-1.5 text-xs text-danger">{{ intervalIssue.message }}</p>
      </SettingRow>
    </template>

  </div>
</template>
