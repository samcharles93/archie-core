<script setup lang="ts">
import { onMounted } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { useLiveResource } from "@/stores/live-updates";
import ConfigSections from "./ConfigSections.vue";
import ConfigUnavailable from "./ConfigUnavailable.vue";
import LifecycleCard from "./LifecycleCard.vue";
import ReadOnlyNotice from "./ReadOnlyNotice.vue";
import { TASKS_SECTIONS } from "./sections";
import { configUnavailable, loadConfig, loadLifecycle } from "./state";

/**
 * The work lifecycle: the statuses and operator actions archied ships, and the
 * budgets every autonomous stage runs under.
 */
async function load(): Promise<void> {
  await Promise.all([loadConfig(), loadLifecycle()]);
}

useLiveResource(null, () => void load());
onMounted(load);
</script>

<template>
  <div>
    <PageHeader title="Task settings" subtitle="How work moves through archied, and what it is allowed to spend." />

    <ReadOnlyNotice />
    <LifecycleCard />
    <ConfigUnavailable v-if="configUnavailable" />
    <ConfigSections v-else :ids="TASKS_SECTIONS" />
  </div>
</template>
