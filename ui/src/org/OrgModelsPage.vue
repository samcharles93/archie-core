<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Plus, Trash2 } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useControlPlaneStore } from "@/stores/control-plane";
import DraftHint from "@/settings/DraftHint.vue";

/**
 * The org's own models: aliases over the instance's, and providers the
 * instance admin lets it add. The server refuses a model the org may not use,
 * and an org provider's key is the org's credential binding named after it.
 */
interface Provider {
  class: string;
  base_url?: string;
  api_key_ref: { engine: string; key: string };
}

const store = useControlPlaneStore();
onMounted(() => store.load());

const aliases = computed(() => store.drafts["model-aliases"]?.value as Record<string, string> | undefined);
const providers = computed(() => store.drafts["provider-settings"]?.value as Record<string, Provider> | undefined);
const errors = computed(() => ["model-aliases", "provider-settings"].map((k) => store.pageErrorFor(k)).filter(Boolean));
const issue = (alias: string) => store.issuesFor("model-aliases").find((entry) => entry.path === alias);

function setAlias(alias: string, model: string) {
  if (!aliases.value) return;
  if (model.trim()) aliases.value[alias] = model.trim();
  else delete aliases.value[alias];
}
const newAlias = ref("");
function addAlias() {
  const name = newAlias.value.trim();
  if (!aliases.value || !name || name in aliases.value) return;
  aliases.value[name] = "";
  newAlias.value = "";
}
const newProvider = ref("");
function addProvider() {
  const name = newProvider.value.trim();
  if (!providers.value || !name || providers.value[name]) return;
  providers.value[name] = { class: "openai", api_key_ref: { engine: "", key: "" } };
  newProvider.value = "";
}
const eyebrow = "mb-2 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase";
</script>

<template>
  <div>
    <PageHeader title="Models" />
    <p v-for="e in errors" :key="String(e)" class="mb-4 text-sm text-danger" role="alert">{{ e }}</p>

    <template v-if="aliases">
      <h2 :class="eyebrow">Aliases</h2>
      <div class="rounded-lg border border-border bg-card">
        <div v-for="(model, alias) in aliases" :key="alias" class="flex flex-wrap items-center gap-3 border-b border-border px-4 py-2.5 last-of-type:border-b-0">
          <label :for="`org-alias-${alias}`" class="w-28 font-mono text-sm font-medium">{{ alias }}</label>
          <Input
            :id="`org-alias-${alias}`"
            :model-value="model"
            class="max-w-md flex-1 font-mono"
            placeholder="provider/model"
            :aria-invalid="issue(String(alias)) ? true : undefined"
            @update:model-value="(v) => setAlias(String(alias), String(v))"
          />
          <DraftHint kind="model-aliases" :path="String(alias)" />
          <span v-if="issue(String(alias))" class="text-xs text-danger">{{ issue(String(alias))?.message }}</span>
          <Button variant="ghost" size="icon" class="ml-auto" :aria-label="`Remove alias ${alias}`" @click="delete aliases[alias]"><Trash2 /></Button>
        </div>
        <form class="flex gap-2 border-t border-border px-4 py-3" @submit.prevent="addAlias">
          <Input v-model="newAlias" class="max-w-56 font-mono" placeholder="alias, e.g. default" aria-label="New alias" />
          <Button type="submit" variant="outline" size="sm" :disabled="!newAlias.trim()"><Plus data-icon="inline-start" />Add alias</Button>
        </form>
      </div>
    </template>

    <template v-if="providers">
      <h2 :class="[eyebrow, 'mt-10']">Providers</h2>
      <div class="rounded-lg border border-border bg-card">
        <div v-for="(provider, name) in providers" :key="name" class="flex flex-wrap items-center gap-3 border-b border-border px-4 py-2.5 last-of-type:border-b-0">
          <span class="w-28 font-mono text-sm font-medium">{{ name }}</span>
          <Input v-model="provider.class" class="w-36 font-mono" :aria-label="`Class for ${name}`" />
          <Input v-model="provider.base_url" class="max-w-md flex-1 font-mono" placeholder="base URL (empty: class default)" :aria-label="`Base URL for ${name}`" />
          <span class="text-xs text-fg-subtle">Key: credential binding "{{ name }}"</span>
          <Button variant="ghost" size="icon" class="ml-auto" :aria-label="`Remove provider ${name}`" @click="delete providers[name]"><Trash2 /></Button>
        </div>
        <form class="flex gap-2 border-t border-border px-4 py-3" @submit.prevent="addProvider">
          <Input v-model="newProvider" class="max-w-56 font-mono" placeholder="provider name" aria-label="New provider" />
          <Button type="submit" variant="outline" size="sm" :disabled="!newProvider.trim()"><Plus data-icon="inline-start" />Add provider</Button>
        </form>
      </div>
    </template>
  </div>
</template>
