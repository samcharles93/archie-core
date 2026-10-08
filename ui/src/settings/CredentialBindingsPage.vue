<script setup lang="ts">
import { computed, onMounted } from "vue";
import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SettingRow } from "@/components/ui/setting-row";
import { useControlPlaneStore } from "@/stores/control-plane";
import ApplyStatusRows from "./ApplyStatusRows.vue";
import HistoryLink from "./HistoryLink.vue";
import SecretRefField from "./SecretRefField.vue";
interface Binding { service: string; org?: string; secret: { engine: string; key: string } }
const KIND = "credential-bindings";
const store = useControlPlaneStore();
onMounted(store.load);
const bindings = computed(() => store.drafts[KIND]?.value as Binding[] | null);
function add() {
 const draft = store.drafts[KIND];
 if (!draft) return;
 if (!draft.value) draft.value = [];
 (draft.value as Binding[]).push({ service: "", org: "", secret: { engine: "", key: "" } });
}
</script>
<template>
 <div>
  <PageHeader title="Credential bindings"><HistoryLink :kinds="[KIND]" /><Button @click="add">Add binding</Button></PageHeader>
  <ApplyStatusRows :kind="KIND" class="mb-4" />
  <p v-if="store.catalogError || store.stateFor(KIND).error" class="mb-4 text-sm text-destructive" role="alert">{{ store.catalogError || store.stateFor(KIND).error }}</p>
  <p v-if="!bindings?.length" class="text-sm text-fg-muted">No credential bindings.</p>
  <section v-for="(binding, index) in bindings" :key="index" class="mb-6 border-t pt-4">
   <div class="flex justify-end"><Button variant="ghost" size="sm" @click="bindings!.splice(index, 1)">Remove binding</Button></div>
   <SettingRow label="Service" :for="`binding-${index}-service`"><Input :id="`binding-${index}-service`" v-model="binding.service" /></SettingRow>
   <SettingRow label="Organisation" :for="`binding-${index}-org`" hint="Empty uses the default organisation."><Input :id="`binding-${index}-org`" v-model="binding.org" /></SettingRow>
   <SettingRow label="Secret"><SecretRefField v-model="binding.secret" :id-prefix="`binding-${index}-secret`" /></SettingRow>
  </section>
 </div>
</template>
