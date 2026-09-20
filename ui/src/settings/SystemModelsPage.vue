<script setup lang="ts">
import { RefreshCw } from "@lucide/vue";
import { onMounted } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import ConfigUnavailable from "./ConfigUnavailable.vue";
import ModelsCard from "./ModelsCard.vue";
import { configUnavailable, loadConfig } from "./state";

/**
 * Which model handles each stage of work, and which providers are wired up.
 * Nothing on this page is editable from the dashboard: a model role is a
 * structured field, and the server keeps those behind its own config path.
 */
onMounted(loadConfig);
</script>

<template>
  <div>
    <PageHeader title="Models" subtitle="Model roles, and the providers backing them.">
      <Button variant="outline" @click="loadConfig">
        <RefreshCw data-icon="inline-start" />
        Refresh
      </Button>
    </PageHeader>

    <ConfigUnavailable v-if="configUnavailable" />
    <ModelsCard v-else />
  </div>
</template>
