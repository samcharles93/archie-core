<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ago } from "@/lib/format";
import { useControlPlaneStore, type AuditEntry } from "@/stores/control-plane";
import { formatValue } from "./changes";
import { filterHistory } from "./history";

/**
 * Every settings change, one row per changed field, newest first. Restore
 * puts a resource back to the version before a change, as an ordinary
 * replace, so the restore is itself recorded here.
 */
const store = useControlPlaneStore();
const { catalog } = storeToRefs(store);
const route = useRoute();
const router = useRouter();

const entries = ref<AuditEntry[]>([]);
const error = ref("");
const expanded = ref<number | null>(null);

const titles = computed(() => new Map(catalog.value.map((r) => [r.kind, r.title])));
const kinds = computed(() => catalog.value.map((r) => r.kind));

async function load(): Promise<void> {
  if (!kinds.value.length) return;
  try {
    entries.value = await store.audit("resources", kinds.value);
    error.value = "";
  } catch (err) {
    error.value = String((err as Error).message || err);
  }
}
onMounted(store.load);
// Any stored version moving means a save happened somewhere: re-read.
const versions = computed(() =>
  kinds.value.map((kind) => store.stateFor(kind).resource?.version).join(","),
);
watch([kinds, versions], load, { immediate: true });

// Filters live in the query, so every History link elsewhere is a deep link.
const ALL = "all";
const section = computed({
  get: () => (route.query.section as string) || ALL,
  set: (value: string) => setQuery({ section: value === ALL ? undefined : value }),
});
const actor = computed({
  get: () => (route.query.actor as string) || ALL,
  set: (value: string) => setQuery({ actor: value === ALL ? undefined : value }),
});
const since = computed({
  get: () => (route.query.since as string) || ALL,
  set: (value: string) => setQuery({ since: value === ALL ? undefined : value }),
});
function setQuery(patch: Record<string, string | undefined>) {
  void router.replace({ query: { ...route.query, ...patch } });
}

const windows: Record<string, number> = { "24h": 86_400_000, "7d": 604_800_000, "30d": 2_592_000_000 };
const actors = computed(() => [...new Set(entries.value.map((e) => e.actor))].sort());
const sectionOptions = computed(() => {
  const options = catalog.value.map((r) => ({ value: r.kind, label: r.title }));
  // A deep link may name several kinds (a page that shows more than one).
  if (section.value !== ALL && !options.some((o) => o.value === section.value))
    options.unshift({
      value: section.value,
      label: section.value.split(",").map((k) => titles.value.get(k) ?? k).join(" + "),
    });
  return options;
});

const rows = computed(() =>
  filterHistory(
    entries.value,
    {
      kinds: section.value === ALL ? [] : section.value.split(","),
      actor: actor.value === ALL ? undefined : actor.value,
      sinceMs: windows[since.value],
    },
    Date.now(),
  ),
);

async function restoreBefore(entry: AuditEntry): Promise<void> {
  const previous = (await store.history(entry.record_key)).find(
    (revision) => revision.version === entry.version - 1,
  );
  if (previous) await store.replace(entry.record_key, previous.value);
}
</script>

<template>
  <div>
    <PageHeader title="History" />
    <p class="-mt-4 mb-6 text-sm text-fg-muted">Every settings change, newest first.</p>

    <div class="mb-4 flex flex-wrap gap-2">
      <Select v-model="section">
        <SelectTrigger class="w-60" aria-label="Section"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem :value="ALL">All sections</SelectItem>
          <SelectItem v-for="o in sectionOptions" :key="o.value" :value="o.value">{{ o.label }}</SelectItem>
        </SelectContent>
      </Select>
      <Select v-model="actor">
        <SelectTrigger class="w-48" aria-label="Changed by"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem :value="ALL">Anyone</SelectItem>
          <SelectItem v-for="a in actors" :key="a" :value="a">{{ a }}</SelectItem>
        </SelectContent>
      </Select>
      <Select v-model="since">
        <SelectTrigger class="w-40" aria-label="When"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem :value="ALL">Any time</SelectItem>
          <SelectItem value="24h">Last 24 hours</SelectItem>
          <SelectItem value="7d">Last 7 days</SelectItem>
          <SelectItem value="30d">Last 30 days</SelectItem>
        </SelectContent>
      </Select>
    </div>

    <p v-if="error" class="text-sm text-danger" role="alert">{{ error }}</p>
    <div v-else class="scroll-fade-x overflow-x-auto rounded-lg border border-border bg-card [scrollbar-width:none]">
      <table class="w-full text-[13px]">
        <thead>
          <tr class="border-b border-border text-left text-[11px] tracking-[0.06em] text-fg-subtle uppercase">
            <th class="px-4 py-2.5 font-medium">When</th>
            <th class="px-4 py-2.5 font-medium">Section</th>
            <th class="px-4 py-2.5 font-medium">Field</th>
            <th class="px-4 py-2.5 font-medium">Change</th>
            <th class="px-4 py-2.5 font-medium">Version</th>
            <th class="px-4 py-2.5 font-medium">Changed by</th>
            <th class="px-4 py-2.5"><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="entry in rows"
            :key="entry.id"
            class="border-b border-border align-top last:border-0"
          >
            <td class="px-4 py-2.5 whitespace-nowrap text-fg-muted" :title="entry.at">
              {{ entry.at ? ago(entry.at) : "" }}
            </td>
            <td class="px-4 py-2.5 whitespace-nowrap">{{ titles.get(entry.record_key) ?? entry.record_key }}</td>
            <td class="px-4 py-2.5 font-mono text-xs">{{ entry.field || "whole value" }}</td>
            <td class="max-w-[22rem] px-4 py-2.5 font-mono text-xs">
              <button
                type="button"
                class="block w-full text-left"
                :aria-expanded="expanded === entry.id"
                @click="expanded = expanded === entry.id ? null : entry.id"
              >
                <span :class="expanded === entry.id ? 'break-all' : 'block truncate'" class="text-danger">
                  − {{ formatValue(entry.old_value ?? undefined) }}
                </span>
                <span :class="expanded === entry.id ? 'break-all' : 'block truncate'" class="text-ok">
                  + {{ formatValue(entry.new_value ?? undefined) }}
                </span>
              </button>
            </td>
            <td class="px-4 py-2.5 font-mono text-xs">{{ entry.version }}</td>
            <td class="px-4 py-2.5 whitespace-nowrap">
              {{ entry.actor }}<span class="text-fg-subtle"> · {{ entry.source }}</span>
            </td>
            <td class="px-2 py-1.5 text-right">
              <Button
                v-if="entry.version > 1"
                variant="ghost"
                size="sm"
                :disabled="store.stateFor(entry.record_key).saving"
                @click="restoreBefore(entry)"
                >Restore</Button
              >
            </td>
          </tr>
          <tr v-if="!rows.length">
            <td colspan="7" class="px-4 py-8 text-center text-sm text-fg-muted">
              {{ entries.length ? "No changes match these filters." : "No changes recorded yet." }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
