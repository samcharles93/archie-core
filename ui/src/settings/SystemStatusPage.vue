<script setup lang="ts">
import { onMounted } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { useLiveResource } from "@/stores/live-updates";
import ConfigNotices from "./ConfigNotices.vue";
import ConfigSections from "./ConfigSections.vue";
import ConfigUnavailable from "./ConfigUnavailable.vue";
import ProvenanceCard from "./ProvenanceCard.vue";
import ReadOnlyNotice from "./ReadOnlyNotice.vue";
import { STATUS_SECTIONS } from "./sections";
import { configUnavailable, loadConfig, loadUpdate, loadVersion } from "./state";
import UpdateActionsCard from "./UpdateActionsCard.vue";
import UpdateStatusCard from "./UpdateStatusCard.vue";

/**
 * What archied is running with right now: whether each component matches what
 * is installed, what is available to update, the files that supplied the
 * running configuration, and the dashboard's own listen address.
 *
 * The address arrives through the generic schema rows rather than a card of
 * its own: it is one field, and a section that grows another should not need a
 * new component here.
 */
async function load(): Promise<void> {
  await Promise.all([loadConfig(), loadVersion(), loadUpdate()]);
}

useLiveResource("updates", () => void load());
onMounted(load);
</script>

<template>
  <div>
    <PageHeader title="Status" subtitle="Runtime health, versions, updates, and configuration." />

    <!-- Only the configuration projection is gated on the config read: update
         checking and the reload notices come from other endpoints, so a config
         that failed to load says so without hiding them. -->
    <ReadOnlyNotice />
    <UpdateStatusCard />
    <UpdateActionsCard />
    <ConfigUnavailable v-if="configUnavailable" />
    <template v-else>
      <ConfigNotices />
      <ConfigSections :ids="STATUS_SECTIONS" />
      <ProvenanceCard />
    </template>
  </div>
</template>
