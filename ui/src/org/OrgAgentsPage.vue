<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Bot, KeyRound, Plus, ShieldCheck, User } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import IdentityGrantsCard from "@/settings/IdentityGrantsCard.vue";
import SettingsSaveBar from "@/settings/SettingsSaveBar.vue";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { StatusPill } from "@/components/ui/status-pill";
import { api } from "@/lib/api";
import { useIdentitiesStore, type Identity } from "@/stores/identities";
import { loadOrg, org, type Member } from "./org";

/**
 * The org's agents and service accounts: their lifecycle, and the org each
 * serves. Credential grants are the control-plane draft below, so the settings
 * save bar rides along with it.
 *
 * An agent's assignment is not readable over the API; the org's member list is,
 * and a bot or service account is a member of the org it serves. The badge
 * reads that, and Assign writes the assignment directly.
 */
const store = useIdentitiesStore();
store.watch();

const serves = ref(new Set<string>());
const error = ref("");
const busy = ref("");

async function load(): Promise<void> {
  if (!org.value) return;
  try {
    const response = await api.orgMembers<{ members: Member[] }>(org.value.id);
    serves.value = new Set(
      response.members
        .filter((m) => !m.workspace_id)
        .map((m) => m.identity_id),
    );
  } catch (cause) {
    error.value = String((cause as Error).message || cause);
  }
}

onMounted(async () => {
  try {
    await loadOrg();
  } catch (cause) {
    error.value = String((cause as Error).message || cause);
    return;
  }
  await load();
});

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

async function assign(value: Identity): Promise<void> {
  if (!org.value) return;
  busy.value = value.id;
  try {
    await api.assignOrgAgent(org.value.id, value.id);
    serves.value = new Set([...serves.value, value.id]);
    error.value = "";
  } catch (cause) {
    error.value = String((cause as Error).message || cause);
  } finally {
    busy.value = "";
  }
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

/** A bot or service account serves an org; people join one as members. */
const isAgent = (value: Identity) =>
  value.kind === "bot" || value.kind === "service_account";
</script>

<template>
  <div>
    <PageHeader title="Agents" />

    <IdentityGrantsCard />

    <p v-if="store.error || error" role="alert" class="mb-4 text-sm text-danger">
      {{ store.error || error }}
    </p>

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
      <Button type="submit" size="sm" :disabled="!name.trim() || !!store.busy"><Plus data-icon="inline-start" />Create</Button>
    </form>

    <ul class="divide-y divide-border rounded-lg border border-border bg-card" aria-label="Agents">
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
        <StatusPill v-if="isAgent(value) && serves.has(value.id)" tone="accent" class="font-mono">
          {{ org?.id }}
        </StatusPill>
        <StatusPill :tone="lifecycle[value.lifecycle].tone" :dot="lifecycle[value.lifecycle].dot" class="capitalize">
          {{ value.lifecycle }}
        </StatusPill>
        <div v-if="value.kind !== 'system' && value.lifecycle !== 'retired'" class="flex gap-1">
          <Button
            v-if="isAgent(value) && !serves.has(value.id)"
            variant="ghost"
            size="sm"
            :title="`Assign to ${org?.name}`"
            :disabled="busy === value.id"
            @click="assign(value)"
            >Assign</Button
          >
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
      <li v-if="!store.identities.length">
        <Empty class="py-6">
          <EmptyHeader>
            <EmptyTitle>No identities yet</EmptyTitle>
          </EmptyHeader>
        </Empty>
      </li>
    </ul>

    <AlertDialog :open="!!retiring" @update:open="(open) => !open && (retiring = null)">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Retire {{ retiring?.display_name }}?</AlertDialogTitle>
          <AlertDialogDescription>This cannot be undone.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive text-destructive-foreground" @click="retire">Retire</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>

    <SettingsSaveBar />
  </div>
</template>
