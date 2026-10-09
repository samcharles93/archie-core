<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { useControlPlaneStore } from "@/stores/control-plane";
import ApplyStatusRows from "./ApplyStatusRows.vue";
import HistoryLink from "./HistoryLink.vue";
interface Playbook { id: string; yaml: string }
const KIND = "eda-playbooks";
const store = useControlPlaneStore();
onMounted(store.load);
const collection = computed(() => store.drafts[KIND]?.value as { playbooks: Playbook[] } | null);
const id = ref("");
function add() {
 const key = id.value.trim();
 if (!key || collection.value?.playbooks.some(entry => entry.id === key)) return;
 const draft = store.drafts[KIND];
 if (!draft) return;
 if (!draft.value) draft.value = { playbooks: [] };
 (draft.value as { playbooks: Playbook[] }).playbooks.push({ id: key, yaml: "" });
 id.value = "";
}
</script>
<template>
 <div>
  <PageHeader title="Playbooks"><HistoryLink :kinds="[KIND]" /></PageHeader>
  <ApplyStatusRows :kind="KIND" class="mb-4" />
  <p v-if="store.catalogError || store.pageErrorFor(KIND)" class="mb-4 text-sm text-destructive" role="alert">{{ store.catalogError || store.pageErrorFor(KIND) }}</p>
  <form class="mb-4 flex gap-2" @submit.prevent="add"><Input v-model="id" aria-label="Playbook ID" placeholder="Playbook ID" class="max-w-xs" /><Button type="submit" :disabled="!id.trim() || collection?.playbooks.some(entry => entry.id === id.trim())">Add playbook</Button></form>
  <p v-if="!collection?.playbooks.length" class="text-sm text-fg-muted">No playbooks installed.</p>
  <section v-for="(entry, index) in collection?.playbooks" :key="index" class="mb-6 border-t pt-4">
   <div class="mb-3 flex items-center justify-between"><label :for="`playbook-${index}`" class="font-mono text-sm">{{ entry.id }}</label><Button variant="ghost" size="sm" @click="collection!.playbooks.splice(index, 1)">Remove playbook</Button></div>
   <Textarea :id="`playbook-${index}`" v-model="entry.yaml" :aria-label="`${entry.id} YAML`" class="min-h-64 font-mono" spellcheck="false" />
  </section>
 </div>
</template>
