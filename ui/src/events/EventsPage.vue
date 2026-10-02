<script setup lang="ts">
import { StatusPill } from "@/components/ui/status-pill";
import { enabled as captureEnabled, loading as captureLoading } from "@/captures/state";
import { computed, onMounted, ref, watch, type Component } from "vue";
import { ArrowRight } from "@lucide/vue";
import { useRoute, useRouter } from "vue-router";

import PageHeader from "@/base/PageHeader.vue";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import BindingsPage from "@/bindings/BindingsPage.vue";
import CapturesPage from "@/captures/CapturesPage.vue";
import { sections } from "@/lib/capabilities";
import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
import MappingsPage from "@/mappings/MappingsPage.vue";
import { activeTab, availableTabs, stepsFor } from "./tab-selection";
import { loadEventCounts, type EventsCounts } from "./counts";

/**
 * The Events surfaces: what arrived, how its fields are read, and which workflow
 * it starts. Three steps on one page rather than three sibling destinations,
 * because they are one chain read in order -- an event, the mapping that names
 * its fields, the binding that decides what runs.
 *
 * The numbered strip is the page's ONLY navigation. It used to be joined by a
 * tab list naming the same three panels a second time ("Inspector | Mappings |
 * Bindings" under "Capture | Map | Bind"), which read as one wizard drawn
 * twice. The strip keeps the verbs; the URL keeps the ids.
 *
 * The open step is the query string's (`?tab=`), so a step is a link and
 * survives a reload, and switching is a `replace`: walking the chain adds no
 * history to back out through. A capture's own selection rides alongside it
 * (`?capture=`), so returning to Capture lands on the record that was open.
 *
 * Which steps exist, and which one is showing, is decided in tab-selection.ts --
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

// One count per step of the chain, for the strip and the tab chips. The reads
// and the counting live in counts.ts, so the strip, the chips and the test all
// read one source rather than three.
const counts = ref<EventsCounts>({});
async function loadCounts() {
  counts.value = await loadEventCounts(api);
}
onMounted(() => void loadCounts());
useLiveResource(null, () => void loadCounts(), 1000);

const steps = computed(() => stepsFor(tabs.value));

/** The panel the active step shows. Undefined when this composition backs none
 * of them, which the template renders as an empty state. */
const panel = computed(() => PANELS[active.value]);
</script>

<template>
  <div>
    <PageHeader title="Events">
      <template v-if="!captureLoading">
        <StatusPill v-if="captureEnabled" dot="live">Listening</StatusPill>
        <StatusPill v-else>Capture off</StatusPill>
      </template>
    </PageHeader>
    <ol class="mb-6 grid grid-cols-1 overflow-hidden rounded-lg border border-border bg-card sm:grid-cols-3" aria-label="How events become work">
      <li v-for="(step, i) in steps" :key="step.id" class="relative border-border not-last:border-b sm:not-last:border-r sm:not-last:border-b-0">
        <button
          type="button"
          class="flex w-full items-center gap-3 px-5 py-4 text-left transition-colors hover:bg-secondary"
          :class="active === step.id && 'bg-secondary'"
          :aria-current="active === step.id ? 'step' : undefined"
          @click="show(step.id)"
        >
          <span class="grid size-7 shrink-0 place-items-center rounded-full border border-border-strong font-mono text-xs text-fg-muted">{{ i + 1 }}</span>
          <span class="min-w-0 flex-1">
            <span class="block text-sm font-medium">{{ step.label }}</span>
            <span class="block text-xs text-fg-subtle">
              <span class="font-mono">{{ counts[step.id] ?? "–" }}</span> {{ step.hint }}
            </span>
          </span>
          <ArrowRight v-if="i < steps.length - 1" class="size-4 text-fg-subtle max-sm:hidden" aria-hidden="true" />
        </button>
      </li>
    </ol>

    <component :is="panel" v-if="panel" :key="active" />
    <Empty v-else>
      <EmptyHeader>
        <EmptyTitle>Event automation is not enabled</EmptyTitle>
      </EmptyHeader>
    </Empty>
  </div>
</template>
