<script setup lang="ts">
import { computed } from "vue";

import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import ConfigCard from "./ConfigCard.vue";
import LifecycleActionList from "./LifecycleActionList.vue";
import LifecycleStatusList from "./LifecycleStatusList.vue";
import { lifecycle, lifecycleError } from "./state";

/**
 * The work lifecycle archied ships: its task statuses and the operator actions
 * that act on them. Both lists come from the server (GET /api/task-meta), so a
 * status or action added on the backend appears here without a frontend change.
 */
const statuses = computed(() => lifecycle.value?.statuses ?? []);
const actions = computed(() => lifecycle.value?.actions ?? []);
</script>

<template>
  <ConfigCard
    v-if="lifecycleError"
    title="Work lifecycle"
    description="The task statuses and operator actions archied ships."
  >
    <Empty>
      <EmptyHeader>
        <EmptyTitle>Lifecycle unavailable</EmptyTitle>
        <EmptyDescription>{{ lifecycleError }}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  </ConfigCard>
  <ConfigCard
    v-else
    title="Work lifecycle"
    description="The task statuses and operator actions archied ships. Add one on the backend and it appears here without a frontend change."
  >
    <LifecycleStatusList :statuses="statuses" />
    <LifecycleActionList :actions="actions" />
  </ConfigCard>
</template>
