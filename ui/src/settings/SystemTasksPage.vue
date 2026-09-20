<script setup lang="ts">
import { onMounted } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { useLiveResource } from "@/stores/live-updates";
import ConfigSections from "./ConfigSections.vue";
import ConfigUnavailable from "./ConfigUnavailable.vue";
import { TASKS_SECTIONS } from "./sections";
import { configUnavailable, loadConfig } from "./state";

useLiveResource(null, () => void loadConfig());
onMounted(loadConfig);
</script>

<template>
  <div>
    <PageHeader title="Task settings" />

    <ConfigUnavailable v-if="configUnavailable" />
    <ConfigSections v-else :ids="TASKS_SECTIONS" />
  </div>
</template>
