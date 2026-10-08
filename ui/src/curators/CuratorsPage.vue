<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import { DurationInput } from "@/components/ui/duration-input";
import { SettingRow } from "@/components/ui/setting-row";
import { useControlPlaneStore } from "@/stores/control-plane";
import ApplyStatusRows from "@/settings/ApplyStatusRows.vue";
import HistoryLink from "@/settings/HistoryLink.vue";
import PageHeader from "@/base/PageHeader.vue";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
import CuratorCard, { type Curator } from "./CuratorCard.vue";

/**
 * Curator observability (archie-core-1786637489932-6). Backed by GET
 * /api/curators, which reads the daemon's live curator registry: which curators
 * are registered, their point-in-time health, and their recent activity, with
 * the reason each action happened.
 */

interface Definition {
 name: string; enabled: boolean; instructions: string; interval: string; cooldown: string;
 on_input: boolean; tools: string[]; skills: boolean; memory_engine: string; conversations: boolean; model: string;
}
const KIND = "curators";
const store = useControlPlaneStore();
onMounted(store.load);
const definitions = computed(() => store.drafts[KIND]?.value as Definition[] | null);
const editing = ref(false);
const newName = ref("");
function add() {
 const name = newName.value.trim();
 if (!name || definitions.value?.some(def => def.name === name)) return;
 const draft = store.drafts[KIND];
 if (!draft) return;
 if (!draft.value) draft.value = [];
 (draft.value as Definition[]).push({name,enabled:false,instructions:"",interval:"1h",cooldown:"0s",on_input:false,tools:[],skills:false,memory_engine:"",conversations:false,model:""});
 newName.value = "";
}
function setTools(def: Definition, value: string | number) { def.tools = String(value).split(",").map(tool => tool.trim()).filter(Boolean); }
watch(() => store.applyStatusFor(KIND).map(row => row.version).join(","), () => void load());
const curators = ref<Curator[]>([]);
const loadError = ref<string | null>(null);

async function load() {
  try {
    const res = await api.curators<{ curators?: Curator[] }>();
    curators.value = res?.curators || [];
    loadError.value = null;
  } catch (err) {
    loadError.value = String((err as Error).message || err);
  }
}

useLiveResource("curators", () => void load());
onMounted(load);
</script>

<template>
  <div>
    <PageHeader title="Curators"><HistoryLink :kinds="[KIND]" /><Button variant="outline" @click="editing = !editing">{{ editing ? "Hide definitions" : "Edit definitions" }}</Button></PageHeader>
    <ApplyStatusRows :kind="KIND" class="mb-4" />
    <p v-if="store.catalogError || store.stateFor(KIND).error" class="mb-4 text-sm text-destructive" role="alert">{{ store.catalogError || store.stateFor(KIND).error }}</p>
    <div v-if="editing" class="mb-6">
      <form class="mb-4 flex gap-2" @submit.prevent="add"><Input v-model="newName" aria-label="Curator name" placeholder="Curator name" class="max-w-xs" /><Button type="submit" :disabled="!newName.trim() || definitions?.some(def => def.name === newName.trim())">Add curator</Button></form>
      <p v-if="!definitions?.length" class="text-sm text-fg-muted">No custom curator definitions.</p>
      <section v-for="(def, index) in definitions" :key="index" class="mb-6 border-t pt-4">
        <div class="flex items-center justify-between"><h2 class="font-mono text-sm">{{ def.name }}</h2><Button variant="ghost" size="sm" @click="definitions!.splice(index, 1)">Remove curator</Button></div>
        <SettingRow label="Enabled"><Switch v-model="def.enabled" :aria-label="`${def.name} enabled`" /></SettingRow>
        <SettingRow label="Instructions" :for="`curator-${index}-instructions`"><Textarea :id="`curator-${index}-instructions`" v-model="def.instructions" /></SettingRow>
        <SettingRow label="Interval"><DurationInput v-model="def.interval" :units="['s', 'm', 'h']" /></SettingRow>
        <SettingRow label="Cooldown"><DurationInput v-model="def.cooldown" :units="['s', 'm', 'h']" /></SettingRow>
        <SettingRow label="Wake on input"><Switch v-model="def.on_input" :aria-label="`${def.name} wake on input`" /></SettingRow>
        <SettingRow label="Tools" :for="`curator-${index}-tools`"><Input :id="`curator-${index}-tools`" :model-value="(def.tools ?? []).join(', ')" @update:model-value="setTools(def, $event)" /></SettingRow>
        <SettingRow label="Skills"><Switch v-model="def.skills" :aria-label="`${def.name} skills`" /></SettingRow>
        <SettingRow label="Memory engine" :for="`curator-${index}-memory`"><Input :id="`curator-${index}-memory`" v-model="def.memory_engine" /></SettingRow>
        <SettingRow label="Conversations"><Switch v-model="def.conversations" :aria-label="`${def.name} conversations`" /></SettingRow>
        <SettingRow label="Model" :for="`curator-${index}-model`"><Input :id="`curator-${index}-model`" v-model="def.model" placeholder="Default model" /></SettingRow>
      </section>
    </div>

    <div
      class="grid grid-cols-1 gap-4 lg:grid-cols-[repeat(auto-fit,minmax(340px,1fr))]"
    >
      <Empty v-if="loadError">
        <EmptyHeader>
          <EmptyTitle>Cannot reach archied</EmptyTitle>
          <EmptyDescription>{{ loadError }}</EmptyDescription>
        </EmptyHeader>
      </Empty>
      <Empty v-else-if="!curators.length">
        <EmptyHeader>
          <EmptyTitle>No curators registered</EmptyTitle>
          <EmptyDescription>
            Curators are background agent loops that maintain memory and skills.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
      <CuratorCard v-for="c in curators" v-else :key="c.name" :curator="c" />
    </div>
  </div>
</template>
