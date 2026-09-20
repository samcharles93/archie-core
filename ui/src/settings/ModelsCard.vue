<script setup lang="ts">
import ConfigCard from "./ConfigCard.vue";
import ModelRoleList from "./ModelRoleList.vue";
import ProviderTable from "./ProviderTable.vue";

/**
 * Which model handles each stage of work, and which LLM providers back them.
 * One card with two lists: the providers exist to serve the roles, so reading
 * them apart would hide the pairing.
 */
</script>

<template>
  <ConfigCard
    title="Models & providers"
    description="Which model handles each stage of work."
  >
    <ModelRoleList />
    <ProviderTable />
  </ConfigCard>
</template>
