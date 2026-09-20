<script setup lang="ts">
import { computed } from "vue";

import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import ConfigCard from "./ConfigCard.vue";
import ConfigList from "./ConfigList.vue";
import ConfigRow from "./ConfigRow.vue";
import { config } from "./state";

/**
 * The files that supplied the running configuration. The order is the
 * precedence order: applied from top to bottom, later entries override earlier
 * ones, which is the one thing a reader of this list needs to know.
 */
const origins = computed(() => config.value?.provenance ?? []);

const rows = computed(() =>
  origins.value.map((origin) => ({
    label: `${origin.layer} ${origin.role}${origin.feature ? ` (${origin.feature})` : ""}`,
    value: origin.path,
  })),
);
</script>

<template>
  <ConfigCard
    title="Configuration sources"
    :description="
      origins.length
        ? 'Applied from top to bottom; later entries take precedence over earlier ones.'
        : 'The files that supplied the running configuration.'
    "
  >
    <Empty v-if="!origins.length">
      <EmptyHeader>
        <EmptyTitle>Source provenance is unavailable.</EmptyTitle>
      </EmptyHeader>
    </Empty>
    <ConfigList v-else>
      <ConfigRow v-for="(row, index) in rows" :key="index" :label="row.label" :value="row.value" />
    </ConfigList>
  </ConfigCard>
</template>
