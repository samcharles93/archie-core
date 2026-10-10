<script setup lang="ts">
import ApplyStatusRows from "./ApplyStatusRows.vue";
import { computed, onMounted, ref, watch } from "vue";
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
import { Switch } from "@/components/ui/switch";
import { instanceAdmin, loadOrg, orgs } from "@/org/org";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import DraftHint from "./DraftHint.vue";
import HistoryLink from "./HistoryLink.vue";
import { availableProviders, aliasModelOptions } from "./model-choices";
import SecretRefField from "./SecretRefField.vue";
import { config, loadConfig } from "./state";

interface Provider {
  class: string;
  api_key_env?: string;
  api_key_ref: { engine: string; key: string };
  base_url?: string;
}

// default runs agent steps and chat when none is named; embedding and
// transcription serve their own purposes. Others are named by steps and chat.
const KNOWN_ALIASES = ["default", "embedding", "transcription"];
// Provider classes the runtime SDK registers.
const CLASSES = ["openai", "anthropic", "azure", "cohere", "deepseek", "gemini", "groq", "mistral", "ollama", "perplexity", "xai"];

const store = useControlPlaneStore();
const { catalog: resourceCatalog, catalogError } = storeToRefs(store);
onMounted(() => Promise.all([store.load(), loadConfig(), loadOrg()]));

const resources = computed(() => resourcesForPage(resourceCatalog.value, "models"));
const providers = computed(() => store.drafts["provider-settings"]?.value as Record<string, Provider> | undefined);
const aliases = computed(() => store.drafts["model-aliases"]?.value as Record<string, string> | undefined);
const errors = computed(() =>
  ["provider-settings", "model-aliases"].map((k) => store.pageErrorFor(k)).filter(Boolean),
);

const modelCatalog = computed(() => config.value?.catalog ?? []);
const configuredIds = computed(() => Object.keys(providers.value ?? {}));
const modelOptions = computed(() => aliasModelOptions(modelCatalog.value, configuredIds.value));
const available = computed(() => availableProviders(modelCatalog.value, configuredIds.value));

const aliasNames = computed(() => [
  ...KNOWN_ALIASES,
  ...Object.keys(aliases.value ?? {}).filter((a) => !KNOWN_ALIASES.includes(a)),
]);
// An unset alias is an absent key: the server refuses an empty model.
function setAlias(alias: string, model: string) {
  if (!aliases.value) return;
  if (model.trim()) aliases.value[alias] = model.trim();
  else delete aliases.value[alias];
}
// Embedding runs through the SDK's embed package, which only these classes
// implement (internal/infrastructure/embedding).
const EMBED_CLASSES = ["openai", "openai-compatible", "gemini", "ollama", "cohere", "mistral"];
const embeddingProviders = computed(() =>
  Object.entries(providers.value ?? {})
    .filter(([, p]) => EMBED_CLASSES.includes(p.class))
    .map(([id]) => id),
);
// The provider is held locally until a model is typed: the alias itself is
// only stored once both halves exist.
const embeddingProvider = ref("");
const embeddingModel = ref("");
watch(
  () => aliases.value?.embedding,
  (value) => {
    if (!value) return;
    embeddingProvider.value = value.split("/")[0] ?? "";
    embeddingModel.value = value.split("/").slice(1).join("/");
  },
  { immediate: true },
);
function setEmbedding(provider: string, model: string) {
  embeddingProvider.value = provider;
  embeddingModel.value = model;
  setAlias("embedding", provider && model ? `${provider}/${model}` : "");
}
const providerKnown = (model: string) => configuredIds.value.includes(model.split("/")[0]!);
const aliasIssues = computed(() => store.issuesFor("model-aliases"));
const aliasIssue = (alias: string) => aliasIssues.value.find((entry) => entry.path === alias);

const newAlias = ref("");
function addAlias() {
  const name = newAlias.value.trim();
  if (!aliases.value || !name || aliasNames.value.includes(name)) return;
  aliases.value[name] = modelOptions.value[0] ?? "";
  newAlias.value = "";
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
// Org access: which instance models each other org may use, and whether it may
// add its own providers. An org with no entry may use nothing.
interface OrgPolicy {
  allowed: string[];
  own_providers: boolean;
}
const policies = computed(() => store.drafts["org-model-policy"]?.value as Record<string, OrgPolicy> | undefined);
const otherOrgs = computed(() => (instanceAdmin.value ? orgs.value.filter((o) => o.id !== "org-sys") : []));
const providerWildcards = computed(() => configuredIds.value.map((id) => `${id}/*`));
function policyFor(id: string): OrgPolicy {
  return policies.value?.[id] ?? { allowed: [], own_providers: false };
}
function setPolicy(id: string, next: Partial<OrgPolicy>) {
  if (!policies.value) return;
  const merged = { ...policyFor(id), ...next };
  if (!merged.allowed.length && !merged.own_providers) delete policies.value[id];
  else policies.value[id] = merged;
}
const allowedText = (id: string) => policyFor(id).allowed.join(", ");
function setAllowed(id: string, text: string) {
  setPolicy(id, { allowed: text.split(",").map((ref) => ref.trim()).filter(Boolean) });
}
const eyebrow = "mb-2 text-[11px] font-medium tracking-[0.06em] text-fg-subtle uppercase";
</script>

<template>
  <div>
    <PageHeader title="Models">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>
    <ApplyStatusRows v-for="resource in resources" :key="resource.kind" :kind="resource.kind" class="mb-4" />

    <p v-for="e in [catalogError, ...errors].filter(Boolean)" :key="String(e)" class="mb-4 text-sm text-danger" role="alert">
      {{ e }}
    </p>

    <template v-if="aliases">
      <h2 :class="eyebrow">Aliases</h2>
      <p v-if="!aliases.default" class="mb-3 text-sm text-warn" role="alert">
        Set the default alias: agent steps and chat that name no alias cannot run without it.
      </p>
      <datalist id="alias-models">
        <option v-for="m in modelOptions" :key="m" :value="m" />
      </datalist>
      <div class="rounded-lg border border-border bg-card">
        <div v-for="alias in aliasNames" :key="alias" class="flex flex-wrap items-center gap-3 border-b border-border px-4 py-2.5 last-of-type:border-b-0">
          <label :for="`alias-${alias}`" class="w-28 text-sm font-medium">{{ alias }}</label>
          <template v-if="alias === 'embedding'">
            <Select :model-value="embeddingProvider" @update:model-value="(v) => setEmbedding(String(v), embeddingModel)">
              <SelectTrigger class="w-44 font-mono" aria-label="Embedding provider"><SelectValue placeholder="unset" /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="id in embeddingProviders" :key="id" :value="id">{{ id }}</SelectItem>
              </SelectContent>
            </Select>
            <Input
              :id="`alias-${alias}`"
              :model-value="embeddingModel"
              class="max-w-64 font-mono"
              placeholder="model"
              :disabled="!embeddingProvider"
              @update:model-value="(v) => setEmbedding(embeddingProvider, String(v))"
            />
            <span v-if="!embeddingProviders.length" class="text-xs text-fg-subtle">Needs an openai, gemini, ollama, cohere or mistral provider.</span>
          </template>
          <template v-else>
            <Input
              :id="`alias-${alias}`"
              :model-value="aliases[alias] ?? ''"
              list="alias-models"
              class="max-w-md flex-1 font-mono"
              placeholder="unset"
              :aria-invalid="aliasIssue(alias) ? true : undefined"
              @update:model-value="(v) => setAlias(alias, String(v))"
            />
            <DraftHint kind="model-aliases" :path="alias" />
            <span v-if="aliasIssue(alias)" class="text-xs text-danger">{{ aliasIssue(alias)?.message }}</span>
            <span v-else-if="aliases[alias] && !providerKnown(aliases[alias])" class="text-xs text-warn">Provider not configured.</span>
          </template>
          <Button v-if="!KNOWN_ALIASES.includes(alias)" variant="ghost" size="icon" class="ml-auto" :aria-label="`Remove alias ${alias}`" @click="delete aliases[alias]">
            <Trash2 />
          </Button>
        </div>
        <form class="flex gap-2 border-t border-border px-4 py-3" @submit.prevent="addAlias">
          <Input v-model="newAlias" class="max-w-56 font-mono" placeholder="alias" aria-label="New alias" />
          <Button type="submit" variant="outline" size="sm" :disabled="!newAlias.trim()"><Plus data-icon="inline-start" />Add alias</Button>
        </form>
      </div>
    </template>

    <template v-if="policies && otherOrgs.length">
      <h2 :class="[eyebrow, 'mt-10']">Org access</h2>
      <datalist id="org-allowed">
        <option v-for="m in [...providerWildcards, ...modelOptions]" :key="m" :value="m" />
      </datalist>
      <div class="rounded-lg border border-border bg-card">
        <div v-for="o in otherOrgs" :key="o.id" class="flex flex-wrap items-center gap-3 border-b border-border px-4 py-2.5 last-of-type:border-b-0">
          <span class="w-28 truncate text-sm font-medium" :title="o.id">{{ o.name }}</span>
          <Input
            :model-value="allowedText(o.id)"
            list="org-allowed"
            class="max-w-md flex-1 font-mono"
            placeholder="no models"
            :aria-label="`Models ${o.name} may use`"
            @update:model-value="(v) => setAllowed(o.id, String(v))"
          />
          <label class="flex items-center gap-2 text-xs text-fg-muted">
            <Switch :model-value="policyFor(o.id).own_providers" @update:model-value="(v) => setPolicy(o.id, { own_providers: !!v })" />
            Own providers
          </label>
          <DraftHint kind="org-model-policy" :path="o.id" />
        </div>
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
                <div class="flex flex-wrap items-center gap-3">
                  <Select v-model="provider.class">
                    <SelectTrigger class="w-48 font-mono" :aria-label="`Class for ${name}`"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem v-for="c in CLASSES.includes(provider.class) ? CLASSES : [provider.class, ...CLASSES]" :key="c" :value="c">{{ c }}</SelectItem>
                    </SelectContent>
                  </Select>
                  <DraftHint kind="provider-settings" :path="`${name}.class`" />
                </div>
              </SettingRow>
              <SettingRow label="Base URL" :for="`prov-${name}-url`" hint="Empty: provider default.">
                <div class="flex flex-wrap items-center gap-3">
                  <Input :id="`prov-${name}-url`" v-model="provider.base_url" class="max-w-md font-mono" />
                  <DraftHint kind="provider-settings" :path="`${name}.base_url`" />

                </div>
              </SettingRow>
              <SettingRow label="API key environment variable" :for="`prov-${name}-env`" hint="Used when no secret reference is set.">
                <div class="flex flex-wrap items-center gap-3">
                  <Input :id="`prov-${name}-env`" v-model="provider.api_key_env" aria-label="API key environment variable" placeholder="API key environment variable" class="max-w-md font-mono" />
                  <DraftHint kind="provider-settings" :path="`${name}.api_key_env`" />
                </div>
              </SettingRow>
              <SettingRow label="API key">
                <SecretRefField
                  v-model="provider.api_key_ref"
                  :id-prefix="`prov-${name}-key`"
                  :fallback="provider.api_key_env ? `env ${provider.api_key_env}` : undefined"
                />
              </SettingRow>
              <div class="flex justify-end pb-1">
                <Button variant="ghost" size="sm" class="text-danger" @click="delete providers[name]"><Trash2 data-icon="inline-start" />Remove</Button>
              </div>
            </AccordionContent>
          </AccordionItem>
        </Accordion>
        <form class="flex flex-wrap items-center gap-2 border-t border-border px-4 py-3" @submit.prevent="addProvider">
          <Input v-model="newProvider" list="available-providers" class="max-w-56 font-mono" placeholder="provider" aria-label="New provider" />
          <Button type="submit" variant="outline" size="sm" :disabled="!newProvider.trim()"><Plus data-icon="inline-start" />Add provider</Button>
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
