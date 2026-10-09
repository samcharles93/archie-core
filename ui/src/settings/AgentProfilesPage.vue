<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SettingRow } from "@/components/ui/setting-row";
import { useControlPlaneStore } from "@/stores/control-plane";
import ApplyStatusRows from "./ApplyStatusRows.vue";
import HistoryLink from "./HistoryLink.vue";

interface Profile { image?: string; kit?: string; adapter?: string; tools?: string[] }
const KIND = "agent-profiles";
const store = useControlPlaneStore();
onMounted(store.load);
const profiles = computed(() => store.drafts[KIND]?.value as Record<string, Profile> | null);
const name = ref("");
function add() {
 const key = name.value.trim();
 if (!key || profiles.value?.[key]) return;
 const draft = store.drafts[KIND];
 if (!draft) return;
 if (!draft.value) draft.value = {};
 (draft.value as Record<string, Profile>)[key] = { image: "", kit: "", adapter: "", tools: [] };
 name.value = "";
}
function tools(profile: Profile, value: string | number) {
 profile.tools = String(value).split(",").map(tool => tool.trim()).filter(Boolean);
}
</script>
<template>
 <div>
  <PageHeader title="Agent profiles"><HistoryLink :kinds="[KIND]" /></PageHeader>
  <ApplyStatusRows :kind="KIND" class="mb-4" />
  <p v-if="store.catalogError || store.pageErrorFor(KIND)" class="mb-4 text-sm text-destructive" role="alert">{{ store.catalogError || store.pageErrorFor(KIND) }}</p>
  <form class="mb-4 flex gap-2" @submit.prevent="add">
   <Input v-model="name" aria-label="Profile name" placeholder="Profile name" class="max-w-xs" />
   <Button type="submit" :disabled="!name.trim() || !!profiles?.[name.trim()]">Add profile</Button>
  </form>
  <p v-if="!Object.keys(profiles ?? {}).length" class="text-sm text-fg-muted">No agent profiles.</p>
  <section v-for="(profile, key) in profiles" :key="key" class="mb-6 border-t pt-4">
   <div class="flex items-center justify-between"><h2 class="font-mono text-sm">{{ key }}</h2><Button variant="ghost" size="sm" @click="delete profiles![key]">Remove profile</Button></div>
   <SettingRow label="Image" :for="`${key}-image`"><Input :id="`${key}-image`" v-model="profile.image" placeholder="Container image" /></SettingRow>
   <SettingRow label="Kit" :for="`${key}-kit`" hint="Pinned digest; leave Image empty when using a Kit."><Input :id="`${key}-kit`" v-model="profile.kit" placeholder="registry/kit@sha256:…" /></SettingRow>
   <SettingRow label="Adapter" :for="`${key}-adapter`"><Input :id="`${key}-adapter`" v-model="profile.adapter" /></SettingRow>
   <SettingRow label="Tools" :for="`${key}-tools`" hint="Comma-separated tool names."><Input :id="`${key}-tools`" :model-value="(profile.tools ?? []).join(', ')" @update:model-value="tools(profile, $event)" /></SettingRow>
  </section>
 </div>
</template>
