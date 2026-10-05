<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Plus } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api } from "@/lib/api";
import { loadOrg, org, type OrgWorkspace } from "./org";

/**
 * The org's workspaces: the division policies and records are scoped to.
 */
const workspaces = ref<OrgWorkspace[]>([]);
const id = ref("");
const name = ref("");
const environment = ref("");
const error = ref("");
const busy = ref(false);

async function load(): Promise<void> {
  if (!org.value) return;
  try {
    workspaces.value = (
      await api.orgWorkspaces<{ workspaces: OrgWorkspace[] }>(org.value.id)
    ).workspaces;
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
  await load();
});

async function create(): Promise<void> {
  if (!org.value || !id.value.trim() || !name.value.trim()) return;
  busy.value = true;
  try {
    await api.createOrgWorkspace(org.value.id, {
      id: id.value.trim(),
      name: name.value.trim(),
      environment: environment.value.trim(),
    });
    id.value = "";
    name.value = "";
    environment.value = "";
    error.value = "";
    await load();
  } catch (cause) {
    error.value = String((cause as Error).message || cause);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div>
    <PageHeader title="Workspaces" />

    <p v-if="error" role="alert" class="mb-4 text-sm text-danger">{{ error }}</p>

    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>ID</TableHead>
          <TableHead>Name</TableHead>
          <TableHead>Environment</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="workspace in workspaces" :key="workspace.id">
          <TableCell class="font-mono text-xs">{{ workspace.id }}</TableCell>
          <TableCell class="font-medium">{{ workspace.name }}</TableCell>
          <TableCell class="text-fg-subtle">{{ workspace.environment || "—" }}</TableCell>
        </TableRow>
        <TableRow v-if="!workspaces.length">
          <TableCell colspan="3">
            <Empty class="py-6">
              <EmptyHeader>
                <EmptyTitle>No workspaces yet</EmptyTitle>
              </EmptyHeader>
            </Empty>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>

    <form class="mt-4 flex flex-wrap items-end gap-2" @submit.prevent="create">
      <Input v-model="id" class="w-40 font-mono" placeholder="id" aria-label="Workspace id" required />
      <Input v-model="name" class="w-56" placeholder="Name" aria-label="Workspace name" required />
      <Input v-model="environment" class="w-40" placeholder="Environment" aria-label="Environment" />
      <Button type="submit" size="sm" :disabled="!id.trim() || !name.trim() || busy">
        <Plus data-icon="inline-start" /> Create
      </Button>
    </form>
  </div>
</template>
