<script setup lang="ts">
import { onMounted } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import DangerousActionsCard from "./DangerousActionsCard.vue";
import { loadDangerous } from "./state";

onMounted(loadDangerous);
</script>

<template>
  <div>
    <PageHeader title="Advanced" />
    <DangerousActionsCard />
  </div>
</template>
