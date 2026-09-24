<script setup lang="ts">
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { useControlPlaneStore } from "@/stores/control-plane";
import { resourcesForPage } from "@/stores/control-plane";
import StructuredResourceCard from "./StructuredResourceCard.vue";

const controlPlane = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(controlPlane);
const resources = computed(() => resourcesForPage(catalog.value, "tasks"));
onMounted(controlPlane.load);
</script>

<template>
  <div>
    <PageHeader title="Task settings" />

    <p v-if="catalogError" class="text-sm text-destructive" role="alert">
      {{ catalogError }}
    </p>
    <StructuredResourceCard
      v-for="descriptor in resources"
      :key="descriptor.kind"
      :descriptor="descriptor"
    />
  </div>
</template>
