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

const omitSetup = computed(() => {
  const kind = setupPanelState(setup.value).kind;
  return kind === "omit" || kind === "dismissed";
});
</script>

<template>
  <DashboardHero />

  <!--
    Each health card takes its own content height rather than the row's.
    Matching them was right while both held a checklist, but once setup is done
    that card is a title and a line while Gate pulse carries a gauge and four
    rows, so equalising them left a mostly-empty box taking a quarter of the
    first screen. No setup panel to show: the pulse spans the row instead.
  -->
  <DashboardSection title="Health">
    <div
      class="grid items-start gap-4"
      :class="omitSetup ? 'grid-cols-1' : 'grid-cols-1 min-[1080px]:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]'"
    >
      <SetupPanel />
      <GatePulseCard />
    </div>
  </DashboardSection>

  <DashboardSection v-if="summary" title="Throughput" note="Across all repositories">
    <ThroughputTiles />
  </DashboardSection>

  <DashboardSection v-if="summary" title="Right now">
    <div class="grid grid-cols-1 gap-4 min-[1080px]:grid-cols-[minmax(0,1fr)_minmax(0,1.5fr)]">
      <TokenOutlookCard />
      <LiveActivityCard />
    </div>
  </DashboardSection>
</template>
