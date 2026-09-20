<script setup lang="ts">
import { onMounted } from "vue";

import Topbar from "@/components/topbar/Topbar.vue";
import { TooltipProvider } from "@/components/ui/tooltip";
import { hidden, loadCapabilities } from "@/lib/capabilities";

onMounted(() => {
  void loadCapabilities();
});
</script>

<template>
  <TooltipProvider :delay-duration="350">
    <Topbar :hidden="hidden" />
    <main class="min-h-[calc(100svh-3.5rem)]">
      <!--
        Keyed on the path and its parameters but not the query: a query-only
        change is an entry state, so the page keeps the operator's filters,
        while two different :id values get fresh instances.
      -->
      <RouterView v-slot="{ Component, route }">
        <component :is="Component" :key="route.path" />
      </RouterView>
    </main>
  </TooltipProvider>
</template>
