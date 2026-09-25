<script setup lang="ts">
import { computed } from "vue";

import DashboardHero from "./DashboardHero.vue";
import GatePulseCard from "./GatePulseCard.vue";
import LiveActivityCard from "./LiveActivityCard.vue";
import NeedsYouCard from "./NeedsYouCard.vue";
import SetupPanel from "./SetupPanel.vue";
import { setupPanelState } from "./setup-preference";
import { setup, summary, useDashboard } from "./state";
import ThroughputTiles from "./ThroughputTiles.vue";
import TokenOutlookCard from "./TokenOutlookCard.vue";

/**
 * What needs you, first: the waiting tasks and live activity in the main
 * column, health and spend in the rail. Composes the panels; it holds no
 * state of its own.
 */
useDashboard();

const setupIncomplete = computed(() => setupPanelState(setup.value).kind === "incomplete");
</script>

<template>
  <DashboardHero />
  <SetupPanel v-if="setupIncomplete" class="mb-6" />
  <ThroughputTiles v-if="summary" class="mb-6" />
  <div class="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_380px]">
    <div class="grid min-w-0 gap-6">
      <NeedsYouCard />
      <LiveActivityCard v-if="summary" />
    </div>
    <div class="grid min-w-0 gap-6">
      <GatePulseCard />
      <TokenOutlookCard v-if="summary" />
    </div>
  </div>
</template>
