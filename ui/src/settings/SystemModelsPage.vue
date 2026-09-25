<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { storeToRefs } from "pinia";
import { Plus, Trash2 } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
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
import SecretRefField from "./SecretRefField.vue";

interface Provider {
  class: string;
  api_key_env?: string;
  api_key_ref: { engine: string; key: string };
  base_url?: string;
  has_credential?: boolean;
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "models"));
const providers = computed(() => store.drafts["provider-settings"]?.value as Record<string, Provider> | undefined);
const roles = computed(() => store.drafts["model-role-assignments"]?.value as Record<string, string> | undefined);
const errors = computed(() =>
  ["provider-settings", "model-role-assignments"].map((k) => store.stateFor(k).error).filter(Boolean),
);

// The roles the daemon reads; others come from workflow steps that name one.
const KNOWN_ROLES = ["builder", "planner", "triage", "embedding"];
// Provider classes the runtime SDK registers.
const CLASSES = ["openai", "anthropic", "azure", "cohere", "deepseek", "gemini", "groq", "mistral", "ollama", "perplexity", "xai"];

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

// A role references "provider/model", and the server refuses anything else.
const roleValid = (model: string) => /^[^/\s]+\/\S+$/.test(model);
const providerKnown = (model: string) => !!providers.value?.[model.split("/")[0]!];

const newProvider = ref("");
function addProvider() {
  const name = newProvider.value.trim();
  if (!providers.value || !name || providers.value[name]) return;
  providers.value[name] = { class: name, api_key_ref: { engine: "", key: "" } };
  newProvider.value = "";
}
const newRole = ref("");
function addRole() {
  const name = newRole.value.trim();
  if (!roles.value || !name || name in roles.value) return;
  roles.value[name] = "provider/model";
  newRole.value = "";
}
const eyebrow = "mb-1 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase";
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
      <SettingRow v-for="role in roleNames" :key="role" :label="role" :for="`role-${role}`">
        <div class="flex items-center gap-2">
          <Input
            :id="`role-${role}`"
            :model-value="roles[role] ?? ''"
            class="max-w-md font-mono"
            placeholder="unset"
            :aria-invalid="(roles[role] && !roleValid(roles[role])) || undefined"
            @update:model-value="(v) => setRole(role, String(v))"
          />
          <Button
            v-if="!KNOWN_ROLES.includes(role)"
            variant="ghost"
            size="icon"
            :aria-label="`Remove role ${role}`"
            @click="delete roles[role]"
            ><Trash2
          /></Button>
        </div>
        <p v-if="roles[role] && !roleValid(roles[role])" class="mt-1.5 text-xs text-danger">Use provider/model.</p>
        <p v-else-if="roles[role] && providers && !providerKnown(roles[role])" class="mt-1.5 text-xs text-warn">
          No provider named {{ roles[role].split("/")[0] }}.
        </p>
      </SettingRow>
      <div class="flex gap-2 border-t border-border py-4">
        <Input v-model="newRole" class="max-w-56 font-mono" placeholder="role name" aria-label="New role" @keydown.enter="addRole" />
        <Button variant="outline" size="sm" :disabled="!newRole.trim()" @click="addRole"><Plus data-icon="inline-start" /> Add role</Button>
      </div>
    </template>

    <template v-if="providers">
      <h2 :class="[eyebrow, 'mt-10']">Providers</h2>
      <section
        v-for="(provider, name) in providers"
        :key="name"
        class="mb-4 rounded-lg border border-border bg-card px-5 pt-4 pb-2"
        :aria-label="String(name)"
      >
        <header class="flex items-center gap-2">
          <h3 class="font-mono text-[15px] font-medium">{{ name }}</h3>
          <Button variant="ghost" size="icon" class="ml-auto" :aria-label="`Remove provider ${name}`" @click="delete providers[name]"><Trash2 /></Button>
        </header>
        <SettingRow label="Class">
          <Select v-model="provider.class">
            <SelectTrigger class="w-48 font-mono" :aria-label="`Class for ${name}`"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem v-for="c in CLASSES.includes(provider.class) ? CLASSES : [provider.class, ...CLASSES]" :key="c" :value="c">{{ c }}</SelectItem>
            </SelectContent>
          </Select>
        </SettingRow>
        <SettingRow label="Base URL" :for="`prov-${name}-url`">
          <Input :id="`prov-${name}-url`" v-model="provider.base_url" class="max-w-md font-mono" />
        </SettingRow>
        <SettingRow label="API key">
          <SecretRefField
            v-model="provider.api_key_ref"
            :id-prefix="`prov-${name}-key`"
            :fallback="provider.api_key_env ? `env ${provider.api_key_env}` : undefined"
          />
        </SettingRow>
      </section>
      <div class="flex gap-2">
        <Input v-model="newProvider" class="max-w-56 font-mono" placeholder="provider name" aria-label="New provider" @keydown.enter="addProvider" />
        <Button variant="outline" size="sm" :disabled="!newProvider.trim()" @click="addProvider"><Plus data-icon="inline-start" /> Add provider</Button>
      </div>
    </template>
  </div>
</template>
