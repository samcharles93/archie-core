<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Plus } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import { StatusPill } from "@/components/ui/status-pill";
import { Switch } from "@/components/ui/switch";
import { grants, stage, useExtensionsStore, type Extension } from "@/stores/extensions";
import ApplyStatusRows from "./ApplyStatusRows.vue";

const KIND = "extension-settings";
const store = useExtensionsStore();
onMounted(store.load);

const available = computed(() => store.catalogue.filter((entry) => !entry.installed));

const name = ref("");
const reference = ref("");
const digest = ref("");
async function install(): Promise<void> {
  if (await store.install(name.value, reference.value, digest.value)) {
    name.value = reference.value = digest.value = "";
  }
}

// The dialog's action closes it before its click handler runs, so the
// target outlives the open flag rather than being cleared on close.
const accepting = ref<Extension | null>(null);
const acceptOpen = ref(false);
async function accept(): Promise<void> {
  if (accepting.value) await store.accept(accepting.value.name);
}
const removing = ref<Extension | null>(null);
const removeOpen = ref(false);
async function remove(): Promise<void> {
  if (removing.value) await store.remove(removing.value.name);
}

const tones = {
  accept: { tone: "warn", dot: "warn", label: "Authority not accepted" },
  disabled: { tone: "neutral", dot: "idle", label: "Disabled" },
  enabled: { tone: "neutral", dot: "live", label: "Enabled" },
  inert: { tone: "neutral", dot: "idle", label: "No extensions" },
} as const;
</script>

<template>
  <div>
    <PageHeader title="Extensions" />

    <p v-if="store.error" role="alert" class="mb-4 text-sm text-danger">{{ store.error }}</p>

    <section class="mb-6" aria-label="Marketplace">
      <h2 class="mb-2 text-sm font-medium">Marketplace</h2>
      <p v-if="store.catalogueError" class="text-sm text-fg-muted">{{ store.catalogueError }}</p>
      <ul v-else class="divide-y divide-border rounded-lg border border-border bg-card">
        <li v-for="entry in available" :key="entry.name" class="flex flex-wrap items-center gap-3 px-4 py-3">
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm font-medium">
              {{ entry.name }}
              <span class="font-mono text-xs text-fg-subtle">{{ entry.surface }} {{ entry.version }}</span>
            </p>
            <p class="text-xs text-fg-subtle">{{ entry.description }}</p>
          </div>
          <Button size="sm" :disabled="!!store.busy" @click="store.installFromCatalogue(entry.name)">
            <Plus data-icon="inline-start" /> Install
          </Button>
        </li>
        <li v-if="!available.length" class="px-4 py-3 text-sm text-fg-muted">Everything listed is installed.</li>
      </ul>
      <details class="mt-3">
        <summary class="cursor-pointer text-xs text-fg-muted">Install by reference</summary>
        <form class="mt-2 flex flex-wrap gap-2" @submit.prevent="install">
          <Input v-model="name" class="max-w-40" placeholder="Name" aria-label="Name" required />
          <Input v-model="reference" class="max-w-80 font-mono" placeholder="registry/repo:tag" aria-label="Reference" required />
          <Input v-model="digest" class="max-w-96 font-mono" placeholder="sha256:…" aria-label="Digest" required />
          <Button type="submit" size="sm" :disabled="!!store.busy"><Plus data-icon="inline-start" /> Install</Button>
        </form>
      </details>
    </section>

    <ul class="divide-y divide-border rounded-lg border border-border bg-card" aria-label="Extensions">
      <li v-for="extension in store.extensions" :key="extension.name" class="flex flex-wrap items-center gap-3 px-4 py-3">
        <div class="min-w-0 flex-1">
          <p class="truncate text-sm font-medium">
            {{ extension.display_name || extension.name }}
            <span class="font-mono text-xs text-fg-subtle">{{ extension.name }} {{ extension.version }}</span>
          </p>
          <p class="text-xs text-fg-subtle">{{ extension.description }}</p>
          <p class="truncate font-mono text-xs text-fg-subtle">{{ extension.digest }}</p>
        </div>
        <StatusPill :tone="tones[stage(extension)].tone" :dot="tones[stage(extension)].dot">
          {{ tones[stage(extension)].label }}
        </StatusPill>
        <Button
          v-if="stage(extension) === 'accept'"
          variant="ghost"
          size="sm"
          :disabled="!!store.busy"
          @click="(accepting = extension), (acceptOpen = true)"
          >Review authority</Button
        >
        <Switch
          v-else-if="stage(extension) !== 'inert'"
          :model-value="extension.enabled"
          :disabled="!!store.busy"
          :aria-label="`Enable ${extension.name}`"
          @update:model-value="(value: boolean) => store.setEnabled(extension.name, value)"
        />
        <Button variant="ghost" size="sm" class="text-danger" :disabled="!!store.busy" @click="(removing = extension), (removeOpen = true)">Remove</Button>
      </li>
      <li v-if="!store.extensions.length">
        <Empty class="py-6">
          <EmptyHeader>
            <EmptyTitle>Nothing installed</EmptyTitle>
          </EmptyHeader>
        </Empty>
      </li>
    </ul>

    <ApplyStatusRows :kind="KIND" class="mt-6 border-t border-border py-4" />

    <AlertDialog v-model:open="acceptOpen">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Accept what {{ accepting?.name }} may use?</AlertDialogTitle>
          <AlertDialogDescription>
            It runs as a process on this host and sees only these.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <ul class="font-mono text-xs">
          <li v-for="line in accepting ? grants(accepting.declared) : []" :key="line">{{ line }}</li>
          <li v-if="accepting && !grants(accepting.declared).length">Nothing beyond its own process.</li>
        </ul>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction @click="accept">Accept</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>

    <AlertDialog v-model:open="removeOpen">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove {{ removing?.name }}?</AlertDialogTitle>
          <AlertDialogDescription>Its process stops on the next sync.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction @click="remove">Remove</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
