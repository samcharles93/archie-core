<script setup lang="ts">
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import ConfigCard from "./ConfigCard.vue";
import DangerousActionsCard from "./DangerousActionsCard.vue";
import StructuredResourceCard from "./StructuredResourceCard.vue";
import { loadDangerous } from "./state";

const controlPlane = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(controlPlane);
const resources = computed(() => resourcesForPage(catalog.value, "advanced"));
async function load(): Promise<void> {
  await Promise.all([controlPlane.load(), loadDangerous()]);
}

onMounted(load);
</script>

<template>
  <div>
    <PageHeader title="Advanced" />

    <p v-if="catalogError" class="text-sm text-destructive" role="alert">
      {{ catalogError }}
    </p>
    <StructuredResourceCard
      v-for="descriptor in resources"
      :key="descriptor.kind"
      :descriptor="descriptor"
    />
    <ConfigCard title="Managed elsewhere">
      <div class="flex flex-wrap gap-2">
        <Button as-child variant="outline"
          ><RouterLink :to="'/events?tab=inspector'"
            >Captured events</RouterLink
          ></Button
        >
        <Button as-child variant="outline"
          ><RouterLink :to="'/events?tab=mappings'"
            >Capture mappings</RouterLink
          ></Button
        >
        <Button as-child variant="outline"
          ><RouterLink :to="'/events?tab=bindings'"
            >Capture bindings</RouterLink
          ></Button
        >
      </div>
    </ConfigCard>
    <DangerousActionsCard />
  </div>
</template>
