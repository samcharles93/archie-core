<script setup lang="ts">
import PageHeader from "@/base/PageHeader.vue";
import ThemeCard from "./ThemeCard.vue";

/**
 * Appearance: how the dashboard looks, on this browser.
 *
 * The theme control moved here out of the topbar icon, and nothing else has
 * followed it yet (docs/prds/dashboard-navigation-groups.md).
 */
</script>

<template>
  <div>
    <PageHeader title="Appearance" subtitle="How the dashboard looks." />
    <ThemeCard />
  </div>
</template>
