<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { storeToRefs } from "pinia";
import { Plus, Trash2 } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
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
import { StatusPill } from "@/components/ui/status-pill";
import {
  TagsInput,
  TagsInputInput,
  TagsInputItem,
  TagsInputItemDelete,
  TagsInputItemText,
} from "@/components/ui/tags-input";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import HistoryLink from "./HistoryLink.vue";

const KIND = "container-runtime-policies";

// The document is config.ContainerConfig on the wire, so its keys are Go
// field names until archie-core-qna6 gives it a snake_case contract.
interface ContainerRuntime {
  Image: string;
  MaxConcurrency: number;
  MaxUptime: string;
  VolumeTTL: string;
  PullPolicy: string;
  Network: string;
  Profiles: Record<string, { Image: string; Tools: string[] | null }> | null;
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
  get: () => runtime.value?.PullPolicy || "missing",
  set: (value: string) => {
    if (runtime.value) runtime.value.PullPolicy = value;
  },
});

const newProfile = ref("");
function addProfile() {
  const name = newProfile.value.trim();
  if (!runtime.value || !name) return;
  runtime.value.Profiles ??= {};
  if (runtime.value.Profiles[name]) return;
  runtime.value.Profiles[name] = { Image: "", Tools: [] };
  newProfile.value = "";
}
</script>

<template>
  <div>
    <PageHeader title="Container runtime">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
      <StatusPill tone="warn">Applies after restart</StatusPill>
    </PageHeader>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">{{ catalogError || error }}</p>

    <template v-if="runtime">
      <SettingRow label="Image" for="cr-image">
        <Input id="cr-image" v-model="runtime.Image" class="font-mono" />
      </SettingRow>
      <SettingRow label="Pull policy">
        <SegmentedControl v-model="pullPolicy" label="Pull policy" :options="pullPolicies" />
      </SettingRow>
      <SettingRow label="Max concurrency" hint="0 = no limit.">
        <NumberField v-model="runtime.MaxConcurrency" :min="0" class="w-32">
          <NumberFieldContent>
            <NumberFieldDecrement />
            <NumberFieldInput class="font-mono" aria-label="Max concurrency" />
            <NumberFieldIncrement />
          </NumberFieldContent>
        </NumberField>
      </SettingRow>
      <SettingRow label="Max uptime" for="cr-uptime">
        <DurationInput id="cr-uptime" v-model="runtime.MaxUptime" :units="['m', 'h']" />
      </SettingRow>
      <SettingRow label="Volume retention" for="cr-ttl">
        <DurationInput id="cr-ttl" v-model="runtime.VolumeTTL" :units="['h']" />
      </SettingRow>
      <SettingRow label="Network" for="cr-network" hint="Empty: auto-detect.">
        <Input id="cr-network" v-model="runtime.Network" class="max-w-sm font-mono" />
      </SettingRow>

      <h2 class="mt-10 mb-1 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase">Profiles</h2>
      <section
        v-for="(profile, name) in runtime.Profiles ?? {}"
        :key="name"
        class="mb-3 rounded-lg border border-border bg-card px-5 pt-3 pb-2"
        :aria-label="`Profile ${name}`"
      >
        <header class="flex items-center">
          <h3 class="flex-1 font-mono text-sm font-medium">{{ name }}</h3>
          <Button variant="ghost" size="icon" :aria-label="`Remove profile ${name}`" @click="delete runtime.Profiles![name]"><Trash2 /></Button>
        </header>
        <SettingRow label="Image" :for="`prof-${name}-image`" hint="Empty: default image.">
          <Input :id="`prof-${name}-image`" v-model="profile.Image" class="font-mono" />
        </SettingRow>
        <SettingRow label="Tools" hint="Empty allows all.">
          <TagsInput
            :model-value="profile.Tools ?? []"
            class="font-mono"
            :aria-label="`Tools for ${name}`"
            @update:model-value="(v) => (profile.Tools = v as string[])"
          >
            <TagsInputItem v-for="tool in profile.Tools ?? []" :key="tool" :value="tool">
              <TagsInputItemText />
              <TagsInputItemDelete />
            </TagsInputItem>
            <TagsInputInput placeholder="tool name" />
          </TagsInput>
        </SettingRow>
      </section>
      <div class="flex gap-2">
        <Input v-model="newProfile" class="max-w-56 font-mono" placeholder="profile name" aria-label="New profile" @keydown.enter="addProfile" />
        <Button variant="outline" size="sm" :disabled="!newProfile.trim()" @click="addProfile"><Plus data-icon="inline-start" /> Add profile</Button>
      </div>
    </template>
  </div>
</template>
