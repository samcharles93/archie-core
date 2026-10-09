<script setup lang="ts">
import ApplyStatusRows from "./ApplyStatusRows.vue";
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { SettingRow } from "@/components/ui/setting-row";
import { Switch } from "@/components/ui/switch";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import DraftHint from "./DraftHint.vue";
import HistoryLink from "./HistoryLink.vue";
import { inForceLabel, reviewDialStatus, type ReviewDials } from "./review";
import { config, loadConfig } from "./state";

const KIND = "review-settings";

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(() => Promise.all([store.load(), loadConfig()]));

const resources = computed(() => resourcesForPage(catalog.value, "review"));
const dials = computed(() => store.drafts[KIND]?.value as ReviewDials | undefined);
const error = computed(() => store.pageErrorFor(KIND));
// What the daemon has in force, read from the published config projection, so
// a draft edit is visibly not yet the effective policy.
const status = computed(() => (dials.value ? reviewDialStatus(config.value, dials.value) : undefined));
</script>

<template>
  <div>
    <PageHeader title="Review">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>
    <ApplyStatusRows v-for="resource in resources" :key="resource.kind" :kind="resource.kind" class="mb-4" />

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">
      {{ catalogError || error }}
    </p>

    <template v-if="dials">
      <SettingRow
        label="Precision gate"
        for="review-precision"
        hint="Drop nitpicks and unverifiable findings before posting. Off favours recall."
      >
        <div class="flex flex-wrap items-center gap-3">
          <Switch id="review-precision" v-model="dials.precision_gate" aria-label="Precision gate" />
          <span class="text-xs text-fg-subtle">
            {{ inForceLabel(status?.precision_gate.inForce)
            }}<template v-if="status?.precision_gate.pending"> · save to apply</template>
          </span>
          <DraftHint :kind="KIND" path="precision_gate" />
        </div>
      </SettingRow>
      <SettingRow
        label="Approve before posting"
        for="review-approve"
        hint="Draft waits for you before it posts."
      >
        <div class="flex flex-wrap items-center gap-3">
          <Switch id="review-approve" v-model="dials.approve_before_post" aria-label="Approve before posting" />
          <span class="text-xs text-fg-subtle">
            {{ inForceLabel(status?.approve_before_post.inForce)
            }}<template v-if="status?.approve_before_post.pending"> · save to apply</template>
          </span>
          <DraftHint :kind="KIND" path="approve_before_post" />
        </div>
      </SettingRow>
    </template>
  </div>
</template>
