<script setup lang="ts">
import { onMounted } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { useLiveResource } from "@/stores/live-updates";
import ConfigUnavailable from "./ConfigUnavailable.vue";
import RepositoriesCard from "./RepositoriesCard.vue";
import { configUnavailable, loadConfig } from "./state";

/**
 * The repositories Archie watches, and the gate overrides each one carries.
 */
useLiveResource(null, () => void loadConfig());
onMounted(loadConfig);
</script>

<template>
  <div>
    <PageHeader title="Repositories" />

    <ConfigUnavailable v-if="configUnavailable" />
    <RepositoriesCard v-else />
  </div>
</template>
