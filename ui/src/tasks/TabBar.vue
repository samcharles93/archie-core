<script setup lang="ts">
import { TabsList, TabsTrigger } from "@/components/ui/tabs";

import { RUN_TABS } from "./task-run";

/**
 * The run page's tab bar. It owns no state and renders no panel: the open tab
 * is the page's, carried in the query string, and this renders the list that
 * switches it. It must sit inside a `Tabs`, which supplies the roving focus
 * and the selection model -- a `TabsTrigger` outside a `TabsList` is not a tab.
 *
 * Keyboard behaviour is the WAI-ARIA tabs pattern with automatic activation,
 * which `Tabs` provides: the list is one stop in the page's tab order,
 * Left/Right move between tabs and select as they go, Home/End jump to the
 * ends. Automatic activation is right here because every panel is a local
 * render, not a fetch: there is no cost to switching.
 */
</script>

<template>
  <TabsList variant="line" class="w-full justify-start">
    <TabsTrigger v-for="tab in RUN_TABS" :key="tab.id" :value="tab.id">{{ tab.label }}</TabsTrigger>
  </TabsList>
</template>
