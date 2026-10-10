<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Plus } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api } from "@/lib/api";
import { useIdentitiesStore } from "@/stores/identities";
import { loadOrg, org, ROLES, type Member, type Role } from "./org";

/**
 * The org's members and the role each holds. A membership is org-wide, or
 * scoped to one workspace when the row names one; changing a role or removing
 * a member touches exactly that membership.
 */
const members = ref<Member[]>([]);
const error = ref("");
const busy = ref("");
const identities = useIdentitiesStore();

const picked = ref("");
const pickedRole = ref<Role>("viewer");
// A person who has not signed in yet, named by email or by the provider's
// subject. They sign in as this role once the provider vouches for them.
const person = ref("");
const personRole = ref<Role>("viewer");

async function load(): Promise<void> {
  if (!org.value) return;
  try {
    members.value = (
      await api.orgMembers<{ members: Member[] }>(org.value.id)
    ).members;
    error.value = "";
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
  identities.watch();
  await load();
});

// Active identities that hold no org-wide membership yet, so Add cannot pick
// someone the table already shows. The system identity is not a person.
const candidates = computed(() => {
  const taken = new Set(
    members.value.filter((m) => !m.workspace_id).map((m) => m.identity_id),
  );
  return identities.identities.filter(
    (i) => i.kind !== "system" && i.lifecycle === "active" && !taken.has(i.id),
  );
});

async function run(key: string, action: () => Promise<void>): Promise<void> {
  busy.value = key;
  try {
    await action();
    error.value = "";
  } catch (cause) {
    error.value = String((cause as Error).message || cause);
  } finally {
    busy.value = "";
  }
}

const add = () =>
  run("add", async () => {
    if (!org.value || !picked.value) return;
    await api.setOrgMember(org.value.id, picked.value, { role: pickedRole.value });
    picked.value = "";
    await load();
  });

const addPerson = () =>
  run("person", async () => {
    const value = person.value.trim();
    if (!org.value || !value) return;
    await api.addOrgMember(org.value.id, {
      ...(value.includes("@") ? { email: value } : { subject: value }),
      role: personRole.value,
    });
    person.value = "";
    await load();
  });

const setRole = (member: Member, role: unknown) =>
  run(member.identity_id + (member.workspace_id ?? ""), async () => {
    if (!org.value) return;
    await api.setOrgMember(org.value.id, member.identity_id, {
      role: String(role),
      workspace: member.workspace_id,
    });
    await load();
  });

const remove = (member: Member) =>
  run(member.identity_id + (member.workspace_id ?? ""), async () => {
    if (!org.value) return;
    await api.removeOrgMember(org.value.id, member.identity_id, member.workspace_id);
    await load();
  });
</script>

<template>
  <div>
    <PageHeader title="Members" />

    <p v-if="error" role="alert" class="mb-4 text-sm text-danger">{{ error }}</p>

    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Member</TableHead>
          <TableHead>Kind</TableHead>
          <TableHead>Workspace</TableHead>
          <TableHead>Role</TableHead>
          <TableHead class="text-right">Actions</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="member in members" :key="member.identity_id + (member.workspace_id ?? '')">
          <TableCell class="font-medium">{{ member.display_name }}</TableCell>
          <TableCell class="text-fg-subtle capitalize">{{ member.kind.replace("_", " ") }}</TableCell>
          <TableCell class="font-mono text-xs text-fg-subtle">{{ member.workspace_id ?? "—" }}</TableCell>
          <TableCell>
            <Select :model-value="member.role" @update:model-value="setRole(member, $event)">
              <SelectTrigger class="w-36" :aria-label="`Role for ${member.display_name}`">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="role in ROLES" :key="role" :value="role">{{ role }}</SelectItem>
              </SelectContent>
            </Select>
          </TableCell>
          <TableCell class="text-right">
            <Button
              variant="ghost"
              size="sm"
              class="text-danger"
              :disabled="busy !== ''"
              @click="remove(member)"
              >Remove</Button
            >
          </TableCell>
        </TableRow>
        <TableRow v-if="!members.length">
          <TableCell colspan="5">
            <Empty class="py-6">
              <EmptyHeader>
                <EmptyTitle>No members yet</EmptyTitle>
              </EmptyHeader>
            </Empty>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>

    <form class="mt-4 flex flex-wrap items-end gap-2" @submit.prevent="add">
      <Select v-model="picked">
        <SelectTrigger class="w-64" aria-label="Identity">
          <SelectValue placeholder="Add a member" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="identity in candidates" :key="identity.id" :value="identity.id">
            {{ identity.display_name }}
          </SelectItem>
        </SelectContent>
      </Select>
      <Select v-model="pickedRole">
        <SelectTrigger class="w-36" aria-label="Role">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="role in ROLES" :key="role" :value="role">{{ role }}</SelectItem>
        </SelectContent>
      </Select>
      <Button type="submit" size="sm" :disabled="!picked || busy !== ''">
        <Plus data-icon="inline-start" /> Add member
      </Button>
    </form>

    <form class="mt-4 flex flex-wrap items-end gap-2" @submit.prevent="addPerson">
      <Input
        v-model="person"
        class="w-64"
        aria-label="Email or provider subject"
        placeholder="Email or provider subject"
      />
      <Select v-model="personRole">
        <SelectTrigger class="w-36" aria-label="Role for the new person">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="role in ROLES" :key="role" :value="role">{{ role }}</SelectItem>
        </SelectContent>
      </Select>
      <Button type="submit" size="sm" :disabled="!person.trim() || busy !== ''">
        <Plus data-icon="inline-start" /> Invite person
      </Button>
    </form>
  </div>
</template>
