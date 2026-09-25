<script setup lang="ts">
import HistoryLink from "@/settings/HistoryLink.vue";
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import StructuredResourceCard from "./StructuredResourceCard.vue";

const controlPlane = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(controlPlane);
const resources = computed(() => resourcesForPage(catalog.value, "models"));
onMounted(controlPlane.load);
</script>

<template>
  <div>
    <PageHeader title="Models">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>

    <p v-if="catalogError" class="text-sm text-destructive" role="alert">
      {{ catalogError }}
    </p>
    <StructuredResourceCard
      v-for="descriptor in resources"
      :key="descriptor.kind"
      :descriptor="descriptor"
      :root-path="
        descriptor.kind === 'provider-settings' ? 'providers' : 'roles'
      "
    />
  </div>
</template>
