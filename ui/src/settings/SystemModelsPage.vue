<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { storeToRefs } from "pinia";
import { Plus, Trash2 } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { SettingRow } from "@/components/ui/setting-row";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import HistoryLink from "./HistoryLink.vue";
import { availableProviders, roleModelOptions } from "./model-choices";
import SecretRefField from "./SecretRefField.vue";
import { config, loadConfig } from "./state";

interface Provider {
  class: string;
  api_key_env?: string;
  api_key_ref: { engine: string; key: string };
  base_url?: string;
}

// The roles the daemon reads; others come from workflow steps that name one.
const KNOWN_ROLES = ["builder", "planner", "triage", "embedding"];
// Provider classes the runtime SDK registers.
const CLASSES = ["openai", "anthropic", "azure", "cohere", "deepseek", "gemini", "groq", "mistral", "ollama", "perplexity", "xai"];

const store = useControlPlaneStore();
const { catalog: resourceCatalog, catalogError } = storeToRefs(store);
onMounted(() => Promise.all([store.load(), loadConfig()]));

const resources = computed(() => resourcesForPage(resourceCatalog.value, "models"));
const providers = computed(() => store.drafts["provider-settings"]?.value as Record<string, Provider> | undefined);
const roles = computed(() => store.drafts["model-role-assignments"]?.value as Record<string, string> | undefined);
const errors = computed(() =>
  ["provider-settings", "model-role-assignments"].map((k) => store.stateFor(k).error).filter(Boolean),
);

const modelCatalog = computed(() => config.value?.catalog ?? []);
const configuredIds = computed(() => Object.keys(providers.value ?? {}));
const modelOptions = computed(() => roleModelOptions(modelCatalog.value, configuredIds.value));
const available = computed(() => availableProviders(modelCatalog.value, configuredIds.value));

const roleNames = computed(() => [
  ...KNOWN_ROLES,
  ...Object.keys(roles.value ?? {}).filter((r) => !KNOWN_ROLES.includes(r)),
]);
// An unset role is an absent key: the server refuses an empty model.
function setRole(role: string, model: string) {
  if (!roles.value) return;
  if (model.trim()) roles.value[role] = model.trim();
  else delete roles.value[role];
}
const roleValid = (model: string) => /^[^/\s]+\/\S+$/.test(model);
const providerKnown = (model: string) => configuredIds.value.includes(model.split("/")[0]!);

const newRole = ref("");
function addRole() {
  const name = newRole.value.trim();
  if (!roles.value || !name || roleNames.value.includes(name)) return;
  roles.value[name] = modelOptions.value[0] ?? "";
  newRole.value = "";
}

// Adding a provider the catalog found usable carries its class, key variable
// and endpoint; any other name starts blank.
const newProvider = ref("");
function addProvider() {
  const id = newProvider.value.trim();
  if (!providers.value || !id || providers.value[id]) return;
  const found = modelCatalog.value.find((p) => p.id === id);
  providers.value[id] = {
    class: found?.class || (CLASSES.includes(id) ? id : "openai"),
    api_key_env: found?.api_key_env,
    base_url: found?.base_url,
    api_key_ref: { engine: "", key: "" },
  };
  newProvider.value = "";
}
const keySource = (p: Provider) =>
  p.api_key_ref.key ? `${p.api_key_ref.engine} / ${p.api_key_ref.key}` : p.api_key_env ? `env ${p.api_key_env}` : "no key";
const eyebrow = "mb-2 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase";
</script>

<template>
  <div>
    <PageHeader title="Models">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>

    <p v-for="e in [catalogError, ...errors].filter(Boolean)" :key="String(e)" class="mb-4 text-sm text-danger" role="alert">
      {{ e }}
    </p>

    <template v-if="roles">
      <h2 :class="eyebrow">Roles</h2>
      <datalist id="role-models">
        <option v-for="m in modelOptions" :key="m" :value="m" />
      </datalist>
      <div class="rounded-lg border border-border bg-card">
        <Accordion type="multiple">
          <AccordionItem v-for="role in roleNames" :key="role" :value="role" class="px-4">
            <AccordionTrigger class="hover:no-underline">
              <span class="flex min-w-0 flex-1 items-center gap-3">
                <span class="w-28 text-left text-sm font-medium">{{ role }}</span>
                <span class="truncate font-mono text-xs" :class="roles[role] ? 'text-fg-muted' : 'text-fg-subtle'">{{ roles[role] || "unset" }}</span>
                <span v-if="roles[role] && !providerKnown(roles[role])" class="text-xs text-warn">provider not configured</span>
              </span>
            </AccordionTrigger>
            <AccordionContent>
              <div class="flex items-center gap-2 pb-1">
                <Input
                  :model-value="roles[role] ?? ''"
                  list="role-models"
                  class="max-w-md font-mono"
                  placeholder="provider/model"
                  :aria-label="`Model for ${role}`"
                  :aria-invalid="(roles[role] && !roleValid(roles[role])) || undefined"
                  @update:model-value="(v) => setRole(role, String(v))"
                />
                <Button v-if="!KNOWN_ROLES.includes(role)" variant="ghost" size="icon" :aria-label="`Remove role ${role}`" @click="delete roles[role]">
                  <Trash2 />
                </Button>
              </div>
              <p v-if="roles[role] && !roleValid(roles[role])" class="text-xs text-danger">Use provider/model.</p>
            </AccordionContent>
          </AccordionItem>
        </Accordion>
        <form class="flex gap-2 border-t border-border px-4 py-3" @submit.prevent="addRole">
          <Input v-model="newRole" class="max-w-56 font-mono" placeholder="role name" aria-label="New role" />
          <Button type="submit" variant="outline" size="sm" :disabled="!newRole.trim()"><Plus data-icon="inline-start" /> Add role</Button>
        </form>
      </div>
    </template>

    <template v-if="providers">
      <h2 :class="[eyebrow, 'mt-10']">Providers</h2>
      <datalist id="available-providers">
        <option v-for="p in available" :key="p.id" :value="p.id">{{ p.name }}</option>
      </datalist>
      <div class="rounded-lg border border-border bg-card">
        <Accordion type="multiple">
          <AccordionItem v-for="(provider, name) in providers" :key="name" :value="String(name)" class="px-4">
            <AccordionTrigger class="hover:no-underline">
              <span class="flex min-w-0 flex-1 items-center gap-3">
                <span class="w-28 text-left font-mono text-sm font-medium">{{ name }}</span>
                <span class="font-mono text-xs text-fg-muted">{{ provider.class }}</span>
                <span class="truncate font-mono text-xs text-fg-subtle">{{ keySource(provider) }}</span>
              </span>
            </AccordionTrigger>
            <AccordionContent>
              <SettingRow label="Class" class="border-t-0 pt-0">
                <Select v-model="provider.class">
                  <SelectTrigger class="w-48 font-mono" :aria-label="`Class for ${name}`"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="c in CLASSES.includes(provider.class) ? CLASSES : [provider.class, ...CLASSES]" :key="c" :value="c">{{ c }}</SelectItem>
                  </SelectContent>
                </Select>
              </SettingRow>
              <SettingRow label="Base URL" :for="`prov-${name}-url`" hint="Empty: provider default.">
                <Input :id="`prov-${name}-url`" v-model="provider.base_url" class="max-w-md font-mono" />
              </SettingRow>
              <SettingRow label="API key">
                <SecretRefField
                  v-model="provider.api_key_ref"
                  :id-prefix="`prov-${name}-key`"
                  :fallback="provider.api_key_env ? `env ${provider.api_key_env}` : undefined"
                />
              </SettingRow>
              <div class="flex justify-end pb-1">
                <Button variant="ghost" size="sm" class="text-danger" @click="delete providers[name]"><Trash2 data-icon="inline-start" /> Remove</Button>
              </div>
            </AccordionContent>
          </AccordionItem>
        </Accordion>
        <form class="flex flex-wrap items-center gap-2 border-t border-border px-4 py-3" @submit.prevent="addProvider">
          <Input v-model="newProvider" list="available-providers" class="max-w-56 font-mono" placeholder="provider" aria-label="New provider" />
          <Button type="submit" variant="outline" size="sm" :disabled="!newProvider.trim()"><Plus data-icon="inline-start" /> Add provider</Button>
          <span v-if="available.length" class="text-xs text-fg-subtle">
            Available:
            <button v-for="p in available" :key="p.id" type="button" class="mr-2 font-mono hover:text-foreground" @click="newProvider = p.id; addProvider()">
              {{ p.id }}
            </button>
          </span>
        </form>
      </div>
    </template>
  </div>
</template>
