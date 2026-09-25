<script setup lang="ts">
import { StatusPill } from "@/components/ui/status-pill";
import { enabled as captureEnabled, loading as captureLoading } from "@/captures/state";
import { computed, onMounted, ref, watch, type Component } from "vue";
import { ArrowRight } from "@lucide/vue";
import { useRoute, useRouter } from "vue-router";

import PageHeader from "@/base/PageHeader.vue";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import BindingsPage from "@/bindings/BindingsPage.vue";
import CapturesPage from "@/captures/CapturesPage.vue";
import { sections } from "@/lib/capabilities";
import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
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
const active = computed(() =>
  activeTab(String(route.query.tab ?? ""), tabs.value),
);

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

// One count per step of the chain, for the strip and the tab chips. A count
// that could not be read stays blank rather than reading as zero.
const counts = ref<Record<string, number | undefined>>({});
async function loadCounts() {
  const [captures, mappings, bindings] = await Promise.allSettled([
    api.captures<{ captures?: unknown[] | null }>(100),
    api.mappings<{ mappings?: unknown[] }>(),
    api.bindings<{ bindings?: unknown[] }>(),
  ]);
  const size = (r: PromiseSettledResult<Record<string, unknown[] | null | undefined>>, key: string) =>
    r.status === "fulfilled" ? (r.value?.[key]?.length ?? 0) : undefined;
  counts.value = {
    inspector: size(captures as never, "captures"),
    mappings: size(mappings as never, "mappings"),
    bindings: size(bindings as never, "bindings"),
  };
}
onMounted(loadCounts);
useLiveResource(null, () => void loadCounts(), 1000);

const steps = [
  { tab: "inspector", label: "Capture", hint: "webhooks received" },
  { tab: "mappings", label: "Map", hint: "field mappings" },
  { tab: "bindings", label: "Bind", hint: "bindings to workflows" },
];
</script>

<template>
  <div>
    <PageHeader title="Events">
      <template v-if="!captureLoading">
        <StatusPill v-if="captureEnabled" dot="live">Listening</StatusPill>
        <StatusPill v-else>Capture off</StatusPill>
      </template>
    </PageHeader>
    <p class="-mt-4 mb-6 text-sm text-fg-muted">
      Webhooks come in, get mapped to event types, and bindings turn them into work.
    </p>
    <ol class="mb-6 grid grid-cols-1 overflow-hidden rounded-lg border border-border bg-card sm:grid-cols-3" aria-label="How events become work">
      <li v-for="(step, i) in steps" :key="step.tab" class="relative border-border not-last:border-b sm:not-last:border-r sm:not-last:border-b-0">
        <button
          type="button"
          class="flex w-full items-center gap-3 px-5 py-4 text-left transition-colors hover:bg-secondary"
          :class="active === step.tab && 'bg-secondary'"
          @click="show(step.tab)"
        >
          <span class="grid size-7 shrink-0 place-items-center rounded-full border border-border-strong font-mono text-xs text-fg-muted">{{ i + 1 }}</span>
          <span class="min-w-0 flex-1">
            <span class="block text-sm font-medium">{{ step.label }}</span>
            <span class="block text-xs text-fg-subtle">
              <span class="font-mono">{{ counts[step.tab] ?? "–" }}</span> {{ step.hint }}
            </span>
          </span>
          <ArrowRight v-if="i < steps.length - 1" class="size-4 text-fg-subtle max-sm:hidden" aria-hidden="true" />
        </button>
      </li>
    </ol>

    <Tabs class="gap-4" :model-value="active" @update:model-value="show">
      <TabsList variant="line" class="justify-start">
        <TabsTrigger v-for="tab in tabs" :key="tab.id" :value="tab.id" class="flex-none px-3">
          {{ tab.label }}
          <span v-if="counts[tab.id] !== undefined" class="ml-1.5 rounded bg-secondary px-1.5 font-mono text-[11px] text-fg-muted">{{
            counts[tab.id]
          }}</span>
        </TabsTrigger>
      </TabsList>
      <TabsContent v-for="tab in tabs" :key="tab.id" :value="tab.id">
        <component :is="PANELS[tab.id]" />
      </TabsContent>
    </Tabs>
  </div>
</template>
