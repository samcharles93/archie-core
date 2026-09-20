<script setup lang="ts">
import { RefreshCw } from "@lucide/vue";
import { onMounted } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
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

onMounted(load);
</script>

<template>
  <div>
    <PageHeader title="Task settings" subtitle="How work moves through archied, and what it is allowed to spend.">
      <Button variant="outline" @click="load">
        <RefreshCw data-icon="inline-start" />
        Refresh
      </Button>
    </PageHeader>

    <ReadOnlyNotice />
    <LifecycleCard />
    <ConfigUnavailable v-if="configUnavailable" />
    <ConfigSections v-else :ids="TASKS_SECTIONS" />
  </div>
</template>
