<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { storeToRefs } from "pinia";
import { Copy, Plus, Trash2 } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SettingRow } from "@/components/ui/setting-row";
import { StatusPill } from "@/components/ui/status-pill";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import HistoryLink from "./HistoryLink.vue";
import {
  duplicatePersona,
  removePersona,
  renamePersona,
  type PersonaCollection,
} from "./personas";

const KIND = "personas";

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "personas"));
const collection = computed(() => store.drafts[KIND]?.value as PersonaCollection | undefined);
const error = computed(() => store.stateFor(KIND).error);

const filter = ref("");
const selected = ref(0);
const shown = computed(() =>
  (collection.value?.personas ?? [])
    .map((persona, index) => ({ persona, index }))
    .filter(({ persona }) => persona.name.toLowerCase().includes(filter.value.trim().toLowerCase())),
);
const current = computed(() => collection.value?.personas[selected.value]);
const isDefault = computed(() => !!current.value && collection.value?.default === current.value.name);
const chars = computed(() => current.value?.prompt.length ?? 0);

function add() {
  const c = collection.value;
  if (!c) return;
  const taken = new Set(c.personas.map((p) => p.name));
  let name = "new-persona";
  for (let n = 2; taken.has(name); n++) name = `new-persona-${n}`;
  c.personas.push({ name, prompt: "" });
  selected.value = c.personas.length - 1;
}
function duplicate() {
  if (collection.value) selected.value = duplicatePersona(collection.value, selected.value);
}
function remove() {
  if (collection.value && removePersona(collection.value, selected.value))
    selected.value = Math.max(0, selected.value - 1);
}
function rename(name: string | number) {
  if (collection.value) renamePersona(collection.value, selected.value, String(name));
}
</script>

<template>
  <div>
    <PageHeader title="Personas">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">
      {{ catalogError || error }}
    </p>

    <div v-if="collection" class="grid gap-6 md:grid-cols-[16rem_1fr]">
      <div class="flex min-w-0 flex-col gap-2">
        <div class="flex gap-2">
          <Input v-model="filter" placeholder="Filter" aria-label="Filter personas" />
          <Button variant="outline" size="icon" aria-label="New persona" @click="add"><Plus /></Button>
        </div>
        <ul class="flex flex-col gap-0.5" aria-label="Personas">
          <li v-for="{ persona, index } in shown" :key="index">
            <button
              type="button"
              :aria-current="index === selected ? 'true' : undefined"
              :class="
                cn(
                  'w-full rounded-md px-3 py-2 text-left transition-colors hover:bg-secondary',
                  index === selected && 'bg-secondary',
                )
              "
              @click="selected = index"
            >
              <span class="flex items-center gap-2">
                <span class="truncate font-mono text-[13px]" :class="index === selected && 'font-medium'">{{
                  persona.name || "unnamed"
                }}</span>
                <StatusPill v-if="collection.default === persona.name" tone="accent" class="h-5">default</StatusPill>
              </span>
              <span class="block truncate text-xs text-fg-subtle">{{ persona.prompt || "No prompt yet" }}</span>
            </button>
          </li>
        </ul>
      </div>

      <section v-if="current" class="min-w-0 rounded-lg border border-border bg-card px-5 pt-4 pb-2" aria-label="Persona">
        <header class="flex items-center gap-2">
          <h2 class="min-w-0 flex-1 truncate font-mono text-[15px] font-medium">{{ current.name || "unnamed" }}</h2>
          <Button variant="ghost" size="sm" @click="duplicate"><Copy data-icon="inline-start" /> Duplicate</Button>
          <Button
            variant="ghost"
            size="sm"
            class="text-danger"
            :disabled="collection.personas.length <= 1"
            @click="remove"
            ><Trash2 data-icon="inline-start" /> Delete</Button
          >
        </header>
        <SettingRow label="Name" for="persona-name">
          <Input id="persona-name" :model-value="current.name" class="max-w-sm font-mono" @update:model-value="rename" />
        </SettingRow>
        <SettingRow label="Default persona">
          <Switch
            :model-value="isDefault"
            :disabled="isDefault"
            aria-label="Default persona"
            @update:model-value="(on: boolean) => on && (collection!.default = current!.name)"
          />
        </SettingRow>
        <div class="border-t border-border py-4">
          <label for="persona-prompt" class="text-[13px] font-medium">System prompt</label>
          <Textarea id="persona-prompt" v-model="current.prompt" :rows="12" class="mt-2 font-mono text-[13px]" />
          <p class="mt-1.5 text-xs text-fg-subtle">{{ chars.toLocaleString() }} chars · ~{{ Math.round(chars / 4).toLocaleString() }} tokens</p>
        </div>
      </section>
    </div>
  </div>
</template>
