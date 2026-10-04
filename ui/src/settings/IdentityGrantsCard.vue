<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Plus, X } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useControlPlaneStore } from "@/stores/control-plane";
import DraftHint from "./DraftHint.vue";

/** The identity-grants document: the root identity's credential services,
 * and per named identity a list that replaces the root's. */
interface IdentityGrants {
  root: string[] | null;
  identities?: Record<string, string[]>;
}

const KIND = "identity-grants";
const store = useControlPlaneStore();
onMounted(() => store.load());

const grants = computed(() => store.drafts[KIND]?.value as IdentityGrants | undefined);
const error = computed(() => store.stateFor(KIND).error);

const split = (text: string) =>
  text
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);

const root = computed({
  get: () => (grants.value?.root ?? []).join(", "),
  set: (text: string) => {
    if (grants.value) grants.value.root = split(text);
  },
});

function setIdentity(name: string, text: string) {
  if (grants.value) grants.value.identities = { ...grants.value.identities, [name]: split(text) };
}
function removeIdentity(name: string) {
  if (!grants.value?.identities) return;
  const { [name]: _, ...rest } = grants.value.identities;
  grants.value.identities = rest;
}
const newName = ref("");
function addIdentity() {
  const name = newName.value.trim();
  if (name && grants.value && !grants.value.identities?.[name]) setIdentity(name, "");
  newName.value = "";
}
</script>

<template>
  <section class="mb-8" aria-labelledby="identity-grants">
    <h2 id="identity-grants" class="mb-1 text-sm font-medium">Credential grants</h2>
    <p class="mb-3 text-xs text-fg-subtle">
      The credential services each identity may use, comma separated. A named identity's list replaces the root's.
    </p>
    <p v-if="error" role="alert" class="mb-3 text-sm text-danger">{{ error }}</p>
    <div v-if="grants" class="divide-y divide-border rounded-lg border border-border bg-card">
      <label class="flex items-center gap-3 px-4 py-2.5">
        <span class="w-40 shrink-0 text-sm">Root identity</span>
        <Input v-model="root" placeholder="github, anthropic" aria-label="Root identity grants" />
        <DraftHint :kind="KIND" path="root" />
      </label>
      <div v-for="(services, name) in grants.identities" :key="name" class="flex items-center gap-3 px-4 py-2.5">
        <span class="w-40 shrink-0 truncate text-sm">{{ name }}</span>
        <Input
          :model-value="services.join(', ')"
          :aria-label="`${name} grants`"
          @update:model-value="(text) => setIdentity(String(name), String(text))"
        />
        <Button variant="ghost" size="sm" :aria-label="`Remove ${name}`" @click="removeIdentity(String(name))"><X /></Button>
      </div>
      <form class="flex items-center gap-3 px-4 py-2.5" @submit.prevent="addIdentity">
        <Input v-model="newName" class="w-40 shrink-0" placeholder="Identity name" aria-label="Identity name" />
        <Button type="submit" variant="ghost" size="sm" :disabled="!newName.trim()"><Plus data-icon="inline-start" /> Add identity</Button>
      </form>
    </div>
  </section>
</template>
