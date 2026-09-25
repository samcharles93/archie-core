<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { storeToRefs } from "pinia";
import { Plus, Trash2 } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SettingRow } from "@/components/ui/setting-row";
import { StatusPill } from "@/components/ui/status-pill";
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

// A role references "provider/model", and the server refuses anything else.
const roleValid = (model: string) => /^[^/\s]+\/\S+$/.test(model);
const providerKnown = (model: string) => !!providers.value?.[model.split("/")[0]!];

// Providers are keyed by name, so a rename moves the entry.
function renameProvider(from: string, to: string) {
  const p = providers.value;
  if (!p || !to || to === from || p[to]) return;
  p[to] = p[from]!;
  delete p[from];
}
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
  roles.value[name] = "";
  newRole.value = "";
}
const eyebrow = "mb-1 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase";
</script>

<template>
  <div>
    <PageHeader title="Models">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
      <StatusPill tone="warn">Applies after restart</StatusPill>
    </PageHeader>
    <p class="-mt-4 mb-8 text-sm text-fg-muted">Which model plays each role, and the providers that serve them.</p>

    <p v-for="e in [catalogError, ...errors].filter(Boolean)" :key="String(e)" class="mb-4 text-sm text-danger" role="alert">
      {{ e }}
    </p>

    <template v-if="roles">
      <h2 :class="eyebrow">Roles</h2>
      <SettingRow v-for="(_, role) in roles" :key="role" :label="String(role)" :for="`role-${role}`">
        <div class="flex items-center gap-2">
          <Input
            :id="`role-${role}`"
            v-model="roles[role]"
            class="max-w-md font-mono"
            placeholder="provider/model"
            :aria-invalid="!roleValid(roles[role] ?? '') || undefined"
          />
          <Button variant="ghost" size="icon" :aria-label="`Remove role ${role}`" @click="delete roles[role]"><Trash2 /></Button>
        </div>
        <p v-if="!roleValid(roles[role] ?? '')" class="mt-1.5 text-xs text-danger">Write it as provider/model.</p>
        <p v-else-if="providers && !providerKnown(roles[role] ?? '')" class="mt-1.5 text-xs text-warn">
          No provider named {{ (roles[role] ?? "").split("/")[0] }} below.
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
          <Input
            :model-value="String(name)"
            class="max-w-56 font-mono font-medium"
            :aria-label="`Provider name ${name}`"
            @change="(e: Event) => renameProvider(String(name), (e.target as HTMLInputElement).value.trim())"
          />
          <Button variant="ghost" size="icon" class="ml-auto" :aria-label="`Remove provider ${name}`" @click="delete providers[name]"><Trash2 /></Button>
        </header>
        <SettingRow label="Class" :for="`prov-${name}-class`" hint="The provider API it speaks (openai, anthropic, gemini, ollama, ...).">
          <Input :id="`prov-${name}-class`" v-model="provider.class" class="max-w-56 font-mono" />
        </SettingRow>
        <SettingRow label="Base URL" :for="`prov-${name}-url`" hint="Empty uses the provider's own endpoint.">
          <Input :id="`prov-${name}-url`" v-model="provider.base_url" class="max-w-md font-mono" />
        </SettingRow>
        <SettingRow label="API key" hint="Where Archie reads the key from.">
          <p v-if="provider.api_key_env && !provider.api_key_ref.key" class="mb-2 text-xs text-fg-muted">
            Passed to agents as <span class="font-mono text-foreground">{{ provider.api_key_env }}</span>; no secret
            reference is stored for it.
          </p>
          <SecretRefField v-model="provider.api_key_ref" :id-prefix="`prov-${name}-key`" />
        </SettingRow>
      </section>
      <div class="flex gap-2">
        <Input v-model="newProvider" class="max-w-56 font-mono" placeholder="provider name" aria-label="New provider" @keydown.enter="addProvider" />
        <Button variant="outline" size="sm" :disabled="!newProvider.trim()" @click="addProvider"><Plus data-icon="inline-start" /> Add provider</Button>
      </div>
    </template>
  </div>
</template>
