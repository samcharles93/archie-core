<script setup lang="ts">
import PageHeader from "@/base/PageHeader.vue";
import ThemeCard from "./ThemeCard.vue";
import TooltipCard from "./TooltipCard.vue";

/**
 * Appearance: how the dashboard looks, on this browser.
 *
 * The theme control moved here out of the topbar icon
 * (docs/prds/dashboard-navigation-groups.md). These are all browser-local:
 * there is no daemon-side appearance, so nothing on this page is submitted.
 * The density and motion preferences the design names are still to come.
 */
</script>

<template>
  <div>
    <PageHeader title="Appearance" />
    <ThemeCard />
    <TooltipCard />
  </div>
</template>
