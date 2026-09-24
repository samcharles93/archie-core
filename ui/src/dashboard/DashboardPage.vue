<script setup lang="ts">
import { computed } from "vue";

import DashboardHero from "./DashboardHero.vue";
import DashboardSection from "./DashboardSection.vue";
import GatePulseCard from "./GatePulseCard.vue";
import LiveActivityCard from "./LiveActivityCard.vue";
import SetupPanel from "./SetupPanel.vue";
import { setupPanelState } from "./setup-preference";
import { setup, summary, useDashboard } from "./state";
import ThroughputTiles from "./ThroughputTiles.vue";
import TokenOutlookCard from "./TokenOutlookCard.vue";

/**
 * The control room: what Archie is working on, what needs you, and what it is
 * spending. Composes the panels; it holds no state of its own.
 */
useDashboard();

const setupIncomplete = computed(
  () => setupPanelState(setup.value).kind === "incomplete",
);
</script>

<template>
  <DashboardHero />

  <!--
    The Health row holds the pulse alone once setup is done: the checklist
    only renders while there is setup work left, so the grid collapses to a
    single column instead of pairing a checklist with an empty neighbour.
  -->
  <DashboardSection title="Health">
    <div
      class="grid items-start gap-4"
      :class="
        setupIncomplete
          ? 'grid-cols-1 lg:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]'
          : 'grid-cols-1'
      "
    >
      <SetupPanel />
      <GatePulseCard />
    </div>
  </DashboardSection>

  <DashboardSection
    v-if="summary"
    title="Throughput"
    note="Across all repositories"
  >
    <ThroughputTiles />
  </DashboardSection>

  <DashboardSection v-if="summary" title="Right now">
    <div
      class="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.5fr)]"
    >
      <TokenOutlookCard />
      <LiveActivityCard />
    </div>
  </DashboardSection>
</template>
