<script setup lang="ts">
import { computed } from "vue";

import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import ConfigList from "./ConfigList.vue";
import ConfigRow from "./ConfigRow.vue";
import { config } from "./state";

/**
 * Which model handles each stage of work. The role keys are the server's
 * (lowercase, underscored) and are read as words here.
 */
const entries = computed(() => Object.entries(config.value?.models ?? {}));

function roleLabel(role: string): string {
  if (!role) return "Unknown role";
  return role.charAt(0).toUpperCase() + role.slice(1).replace(/_/g, " ");
}
</script>

<template>
  <h3 class="mt-5 mb-2 text-sm font-semibold text-fg-muted first:mt-0">
    Model roles
  </h3>
  <Empty v-if="!entries.length">
    <EmptyHeader>
      <EmptyTitle>No model roles configured</EmptyTitle>
      <EmptyDescription
        >Assign a model to at least one role (e.g. "builder") in
        [models].</EmptyDescription
      >
    </EmptyHeader>
  </Empty>
  <ConfigList v-else>
    <ConfigRow
      v-for="[role, model] in entries"
      :key="role"
      :label="roleLabel(role)"
      :value="model"
    />
  </ConfigList>
</template>
