<script setup lang="ts">
import PageHeader from "@/base/PageHeader.vue";
import HealthStatusCard from "./HealthStatusCard.vue";
import VersionsCard from "./VersionsCard.vue";
</script>

<template>
  <div>
    <PageHeader title="Status" />

    <HealthStatusCard />
    <VersionsCard class="mt-4" />
  </div>
</template>
