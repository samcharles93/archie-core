<script setup lang="ts">
import { ref } from "vue";
import { Bot, KeyRound, Plus, ShieldCheck, User } from "@lucide/vue";

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
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { StatusPill } from "@/components/ui/status-pill";
import { useIdentitiesStore, type Identity } from "@/stores/identities";

const store = useIdentitiesStore();
store.watch();

const name = ref("");
const kind = ref<Identity["kind"]>("bot");
async function create(): Promise<void> {
  if (await store.create(name.value.trim(), kind.value)) name.value = "";
}

const renaming = ref<string | null>(null);
const draftName = ref("");
function startRename(value: Identity) {
  renaming.value = value.id;
  draftName.value = value.display_name;
}
async function finishRename(value: Identity) {
  // Enter and the blur that follows it both land here; only the first commits.
  if (renaming.value !== value.id) return;
  const next = draftName.value.trim();
  renaming.value = null;
  if (next && next !== value.display_name) await store.command(value, "rename", next);
}

const retiring = ref<Identity | null>(null);
async function retire() {
  if (retiring.value) await store.command(retiring.value, "retire");
  retiring.value = null;
}

const icons = { system: ShieldCheck, bot: Bot, service_account: KeyRound, user: User };
const lifecycle = {
  active: { tone: "neutral", dot: "live" },
  suspended: { tone: "warn", dot: "warn" },
  retired: { tone: "neutral", dot: "idle" },
} as const;
</script>

<template>
  <div>
    <PageHeader title="Identities" />
    <p class="-mt-4 mb-8 text-sm text-fg-muted">The actors Archie acts as and on behalf of. Changes apply at once.</p>

    <p v-if="store.error" role="alert" class="mb-4 text-sm text-danger">{{ store.error }}</p>

    <form class="mb-6 flex flex-wrap gap-2" @submit.prevent="create">
      <Input v-model="name" class="max-w-64" placeholder="Display name" aria-label="Display name" required />
      <Select v-model="kind">
        <SelectTrigger class="w-44" aria-label="Kind"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="bot">Bot</SelectItem>
          <SelectItem value="service_account">Service account</SelectItem>
          <SelectItem value="user">User</SelectItem>
        </SelectContent>
      </Select>
      <Button type="submit" size="sm" :disabled="!name.trim() || !!store.busy"><Plus data-icon="inline-start" /> Create</Button>
    </form>

    <ul class="divide-y divide-border rounded-lg border border-border bg-card" aria-label="Identities">
      <li v-for="value in store.identities" :key="value.id" class="flex flex-wrap items-center gap-3 px-4 py-3">
        <span class="grid size-8 shrink-0 place-items-center rounded-md bg-secondary text-muted-foreground">
          <component :is="icons[value.kind]" class="size-4" aria-hidden="true" />
        </span>
        <div class="min-w-0 flex-1">
          <Input
            v-if="renaming === value.id"
            v-model="draftName"
            class="max-w-64"
            aria-label="Display name"
            autofocus
            @keydown.enter="finishRename(value)"
            @keydown.esc="renaming = null"
            @blur="finishRename(value)"
          />
          <p v-else class="truncate text-sm font-medium" :class="value.lifecycle === 'retired' && 'text-fg-subtle'">
            {{ value.display_name }}
          </p>
          <p class="text-xs text-fg-subtle">{{ value.kind.replace("_", " ") }}</p>
        </div>
        <StatusPill :tone="lifecycle[value.lifecycle].tone" :dot="lifecycle[value.lifecycle].dot" class="capitalize">
          {{ value.lifecycle }}
        </StatusPill>
        <div v-if="value.kind !== 'system' && value.lifecycle !== 'retired'" class="flex gap-1">
          <Button variant="ghost" size="sm" :disabled="!!store.busy" @click="startRename(value)">Rename</Button>
          <Button
            v-if="value.lifecycle === 'active'"
            variant="ghost"
            size="sm"
            :disabled="!!store.busy"
            @click="store.command(value, 'suspend')"
            >Suspend</Button
          >
          <Button v-else variant="ghost" size="sm" :disabled="!!store.busy" @click="store.command(value, 'reactivate')"
            >Reactivate</Button
          >
          <Button variant="ghost" size="sm" class="text-danger" :disabled="!!store.busy" @click="retiring = value">Retire</Button>
        </div>
      </li>
      <li v-if="!store.identities.length" class="px-4 py-8 text-center text-sm text-fg-muted">No identities yet.</li>
    </ul>

    <AlertDialog :open="!!retiring" @update:open="(open) => !open && (retiring = null)">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Retire {{ retiring?.display_name }}?</AlertDialogTitle>
          <AlertDialogDescription>A retired identity cannot act again, and this cannot be undone.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive text-destructive-foreground" @click="retire">Retire</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
