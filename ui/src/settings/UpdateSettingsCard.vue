<script setup lang="ts">
import { computed, onMounted } from "vue";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { SettingRow } from "@/components/ui/setting-row";
import { useControlPlaneStore } from "@/stores/control-plane";

const KIND = "update-settings";
type UpdateSettings = { channel: string; pin: string };

const store = useControlPlaneStore();
onMounted(store.load);

const draft = computed(() => store.drafts[KIND]?.value as UpdateSettings | undefined);
const error = computed(() => store.pageErrorFor(KIND));

const channels = [
  { value: "stable", label: "Stable" },
  { value: "next", label: "Prereleases" },
  { value: "exact-pin", label: "Pinned" },
];
const channel = computed({
  get: () => draft.value?.channel || "stable",
  set: (value: string) => {
    if (draft.value) draft.value.channel = value;
  },
});
</script>

<template>
  <Card v-if="draft">
    <CardHeader>
      <CardTitle>Updates</CardTitle>
    </CardHeader>
    <CardContent>
      <p v-if="error" class="mb-2 text-sm text-danger" role="alert">{{ error }}</p>
      <SettingRow label="Release channel">
        <div class="flex justify-end">
          <SegmentedControl v-model="channel" label="Release channel" :options="channels" />
        </div>
      </SettingRow>
      <SettingRow v-if="channel === 'exact-pin'" label="Version" for="update-pin">
        <div class="flex justify-end">
          <Input id="update-pin" v-model="draft.pin" class="max-w-40 font-mono" placeholder="1.48.0" />
        </div>
      </SettingRow>
    </CardContent>
  </Card>
</template>
