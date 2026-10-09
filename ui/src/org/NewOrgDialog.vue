<script setup lang="ts">
import { computed, ref } from "vue";
import { Plus } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { api } from "@/lib/api";
import { useIdentitiesStore } from "@/stores/identities";
import { loadOrg, viewOrg, type Org } from "./org";

/** Creates an org with its default workspace and first owner, then opens it. */
const open = ref(false);
const id = ref("");
const name = ref("");
const owner = ref("");
const error = ref("");
const busy = ref(false);
const identities = useIdentitiesStore();

const people = computed(() =>
  identities.identities.filter((i) => i.kind !== "system" && i.lifecycle === "active"),
);

function opened(value: boolean): void {
  open.value = value;
  if (value) identities.watch();
}

async function create(): Promise<void> {
  busy.value = true;
  try {
    const created = await api.orgCreate<{ org: Org }>({
      id: id.value.trim(),
      name: name.value.trim(),
      owner_identity: owner.value,
    });
    await loadOrg(true);
    viewOrg(created.org.id);
    open.value = false;
    id.value = name.value = owner.value = error.value = "";
  } catch (cause) {
    error.value = String((cause as Error).message || cause);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="opened">
    <DialogTrigger as-child>
      <Button size="sm" variant="outline"><Plus data-icon="inline-start" />New org</Button>
    </DialogTrigger>
    <DialogContent>
      <DialogHeader>
        <DialogTitle>New org</DialogTitle>
      </DialogHeader>
      <form id="new-org" class="flex flex-col gap-3" @submit.prevent="create">
        <Input v-model="name" placeholder="Name" aria-label="Org name" required />
        <Input v-model="id" class="font-mono" placeholder="id" aria-label="Org id" required />
        <Select v-model="owner">
          <SelectTrigger class="w-full" aria-label="Owner">
            <SelectValue placeholder="Owner" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="person in people" :key="person.id" :value="person.id">
              {{ person.display_name }}
            </SelectItem>
          </SelectContent>
        </Select>
        <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
      </form>
      <DialogFooter>
        <Button
          type="submit"
          form="new-org"
          :disabled="!id.trim() || !name.trim() || !owner || busy"
        >
          Create
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
