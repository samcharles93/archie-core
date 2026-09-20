<script setup lang="ts">
import { computed } from "vue";

import DashboardActions from "./DashboardActions.vue";
import { setup } from "./state";

function greeting(): string {
  const h = new Date().getHours();
  if (h < 5) return "Still up?";
  if (h < 12) return "Good morning";
  if (h < 18) return "Good afternoon";
  return "Good evening";
}

const currentGreeting = computed(() => (setup.value?.operator ? `${greeting()}, ${setup.value.operator}` : greeting()));
</script>

<template>
  <div class="mb-5 flex flex-wrap items-start justify-between gap-5">
    <h1 class="text-3xl font-semibold tracking-[-0.03em]">{{ currentGreeting }}</h1>
    <div class="flex flex-wrap items-center gap-2">
      <DashboardActions />
    </div>
  </div>
</template>
