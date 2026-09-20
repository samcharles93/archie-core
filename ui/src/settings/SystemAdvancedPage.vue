<script setup lang="ts">
import { onMounted } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { useLiveResource } from "@/stores/live-updates";
import ConfigSections from "./ConfigSections.vue";
import ConfigUnavailable from "./ConfigUnavailable.vue";
import DangerousActionsCard from "./DangerousActionsCard.vue";
import { ADVANCED_SECTIONS } from "./sections";
import { configUnavailable, loadConfig, loadDangerous } from "./state";

/**
 * The settings that change where archied keeps its state and how it isolates
 * task execution, who it is on the forge, and the actions that need explicit
 * approval before they run.
 *
 * These are the fields with the longest reach: identity is what commits are
 * attributed to, and the container fields decide what the agent runs in.
 */
async function load(): Promise<void> {
  await Promise.all([loadConfig(), loadDangerous()]);
}

useLiveResource(null, () => void load());
onMounted(load);
</script>

<template>
  <div>
    <PageHeader title="Advanced" />

    <ConfigUnavailable v-if="configUnavailable" />
    <ConfigSections v-else :ids="ADVANCED_SECTIONS" />
    <!-- Dangerous actions come from the daemon's own endpoint, not the config
         projection, so a config that failed to load does not hide them. -->
    <DangerousActionsCard />
  </div>
</template>
