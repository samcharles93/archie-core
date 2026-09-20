<script setup lang="ts">
import { RefreshCw } from "@lucide/vue";
import { onMounted } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import ConfigUnavailable from "./ConfigUnavailable.vue";
import ReadOnlyNotice from "./ReadOnlyNotice.vue";
import RepositoriesCard from "./RepositoriesCard.vue";
import { configUnavailable, loadConfig } from "./state";

/**
 * The repositories Archie watches, and the gate overrides each one carries.
 */
onMounted(loadConfig);
</script>

<template>
  <div>
    <PageHeader title="Repositories" subtitle="The repositories Archie polls, and their per-repo gate overrides.">
      <Button variant="outline" @click="loadConfig">
        <RefreshCw data-icon="inline-start" />
        Refresh
      </Button>
    </PageHeader>

    <ReadOnlyNotice />
    <ConfigUnavailable v-if="configUnavailable" />
    <RepositoriesCard v-else />
  </div>
</template>
