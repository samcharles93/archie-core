<script setup lang="ts">
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
import HistoryLink from "./HistoryLink.vue";

const KIND = "container-runtime-policies";

// The stored document is containerRuntimePolicies on the wire, with snake_case
// keys and durations as Go duration strings (archie-core-qna6). Named agent
// profiles are no longer part of it: they moved to the agent-profiles resource.
interface ContainerRuntime {
  image: string;
  max_concurrency: number;
  max_uptime: string;
  volume_ttl: string;
  pull_policy: string;
  network: string;
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "containers"));
const runtime = computed(() => store.drafts[KIND]?.value as ContainerRuntime | undefined);
const error = computed(() => store.stateFor(KIND).error);

const pullPolicies = [
  { value: "missing", label: "If missing" },
  { value: "always", label: "Always" },
];
const pullPolicy = computed({
  get: () => runtime.value?.pull_policy || "missing",
  set: (value: string) => {
    if (runtime.value) runtime.value.pull_policy = value;
  },
});
</script>

<template>
  <div>
    <PageHeader title="Container runtime">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">{{ catalogError || error }}</p>

    <template v-if="runtime">
      <SettingRow label="Image" for="cr-image">
        <div class="flex flex-wrap items-center gap-3">
          <Input id="cr-image" v-model="runtime.image" class="font-mono" />
          <DraftHint :kind="KIND" path="image" />
        </div>
      </SettingRow>
      <SettingRow label="Pull policy">
        <div class="flex flex-wrap items-center gap-3">
          <SegmentedControl v-model="pullPolicy" label="Pull policy" :options="pullPolicies" />
          <DraftHint :kind="KIND" path="pull_policy" />
        </div>
      </SettingRow>
      <SettingRow label="Max concurrency" hint="0 = no limit.">
        <div class="flex flex-wrap items-center gap-3">
          <NumberField v-model="runtime.max_concurrency" :min="0" class="w-32">
            <NumberFieldContent>
              <NumberFieldDecrement />
              <NumberFieldInput class="font-mono" aria-label="Max concurrency" />
              <NumberFieldIncrement />
            </NumberFieldContent>
          </NumberField>
          <DraftHint :kind="KIND" path="max_concurrency" />
        </div>
      </SettingRow>
      <SettingRow label="Max uptime" for="cr-uptime">
        <div class="flex flex-wrap items-center gap-3">
          <DurationInput id="cr-uptime" v-model="runtime.max_uptime" :units="['m', 'h']" />
          <DraftHint :kind="KIND" path="max_uptime" />
        </div>
      </SettingRow>
      <SettingRow label="Volume retention" for="cr-ttl">
        <div class="flex flex-wrap items-center gap-3">
          <DurationInput id="cr-ttl" v-model="runtime.volume_ttl" :units="['h']" />
          <DraftHint :kind="KIND" path="volume_ttl" />
        </div>
      </SettingRow>
      <SettingRow label="Network" for="cr-network" hint="Empty: auto-detect.">
        <div class="flex flex-wrap items-center gap-3">
          <Input id="cr-network" v-model="runtime.network" class="max-w-sm font-mono" />
          <DraftHint :kind="KIND" path="network" />
        </div>
      </SettingRow>
    </template>
  </div>
</template>
