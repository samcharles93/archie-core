<script setup lang="ts">
import { computed, watch, type Component } from "vue";
import { useRoute, useRouter } from "vue-router";

import PageHeader from "@/base/PageHeader.vue";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import BindingsPage from "@/bindings/BindingsPage.vue";
import CapturesPage from "@/captures/CapturesPage.vue";
import { sections } from "@/lib/capabilities";
import MappingsPage from "@/mappings/MappingsPage.vue";
import { activeTab, availableTabs } from "./tab-selection";

/**
 * The Events surfaces: what arrived, how its fields are read, and which workflow
 * it starts. Three tabs on one page rather than three sibling destinations,
 * because they are one chain read in order -- an event, the mapping that names
 * its fields, the binding that decides what runs.
 *
 * The open tab is the query string's (`?tab=`), so a tab is a link and survives
 * a reload, and switching is a `replace`: walking the tabs adds no history to
 * back out through. A capture's own selection rides alongside it (`?capture=`),
 * so returning to the Inspector tab lands on the record that was open.
 *
 * Which tabs exist, and which one is showing, is decided in tab-selection.ts --
 * this file owns the components and the address bar, that file owns the rule,
 * and the rule is unit-tested without a browser.
 */
const PANELS: Record<string, Component> = {
  inspector: CapturesPage,
  bindings: BindingsPage,
  mappings: MappingsPage,
};

const route = useRoute();
const router = useRouter();

const tabs = computed(() => availableTabs(sections.value));
const active = computed(() => activeTab(String(route.query.tab ?? ""), tabs.value));

function show(id: unknown): void {
  const next = String(id);
  if (!next || next === String(route.query.tab ?? "")) return;
  void router.replace({ query: { ...route.query, tab: next } });
}

// A URL naming a tab this composition cannot back -- a bookmark from a fuller
// deployment, or a capability that went away -- is written back to the tab that
// is actually showing, so the address bar never describes a panel that is not
// there.
watch(active, (tab) => show(tab), { immediate: true });
</script>

<template>
  <div>
    <PageHeader title="Events" />
    <Tabs class="gap-4" :model-value="active" @update:model-value="show">
      <TabsList variant="line" class="w-full justify-start">
        <TabsTrigger v-for="tab in tabs" :key="tab.id" :value="tab.id">{{ tab.label }}</TabsTrigger>
      </TabsList>
      <TabsContent v-for="tab in tabs" :key="tab.id" :value="tab.id">
        <component :is="PANELS[tab.id]" />
      </TabsContent>
    </Tabs>
  </div>
</template>
