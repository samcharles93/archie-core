<script setup lang="ts">
import PageHeader from "@/base/PageHeader.vue";
import HealthStatusCard from "./HealthStatusCard.vue";
import ServicesCard from "./ServicesCard.vue";
import UpdateSettingsCard from "./UpdateSettingsCard.vue";
import VersionsCard from "./VersionsCard.vue";
</script>

<template>
  <div>
    <PageHeader title="Status" />

    <ServicesCard />
    <HealthStatusCard />
    <VersionsCard class="mt-4" />
    <UpdateSettingsCard class="mt-4" />
  </div>
</template>
