<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";

type Level = "instance" | "org" | "workspace" | "object";

interface Policy {
  id: string;
  level: Level;
  org_id?: string;
  workspace_id?: string;
  object_kind?: string;
  object_id?: string;
  text: string;
}

const policies = ref<Policy[]>([]);
const shipped = ref<string[]>([]);
const error = ref<string | null>(null);
const saving = ref(false);

const blank = (): Policy => ({ id: "", level: "org", text: "" });
const draft = ref<Policy>(blank());
const editing = ref(false);

const scope = (p: Policy) =>
  p.level === "workspace"
    ? p.workspace_id
    : p.level === "object"
      ? `${p.object_kind}/${p.object_id}`
      : "";

const canSave = computed(
  () =>
    draft.value.id.trim() !== "" &&
    draft.value.text.trim() !== "" &&
    (draft.value.level !== "workspace" || !!draft.value.workspace_id) &&
    (draft.value.level !== "object" ||
      (!!draft.value.object_kind && !!draft.value.object_id)),
);

async function load(): Promise<void> {
  try {
    const response = await api.policies<{ policies: Policy[]; shipped: string[] }>();
    policies.value = response.policies;
    shipped.value = response.shipped;
    error.value = null;
  } catch (err) {
    error.value = (err as Error).message;
  }
}

function edit(p: Policy): void {
  draft.value = { ...p };
  editing.value = true;
}

async function save(): Promise<void> {
  saving.value = true;
  try {
    await api.putPolicy(draft.value);
    draft.value = blank();
    editing.value = false;
    await load();
  } catch (err) {
    error.value = (err as Error).message;
  } finally {
    saving.value = false;
  }
}

async function remove(p: Policy): Promise<void> {
  try {
    await api.deletePolicy(p);
    await load();
  } catch (err) {
    error.value = (err as Error).message;
  }
}

onMounted(load);
</script>

<template>
  <div>
    <PageHeader title="Access policies" />

    <p v-if="error" class="mb-4 text-sm text-destructive">{{ error }}</p>

    <Card class="mb-4">
      <CardHeader>
        <CardTitle>Policies</CardTitle>
      </CardHeader>
      <CardContent class="flex flex-col gap-3">
        <div
          v-for="p in policies"
          :key="`${p.level}/${scope(p)}/${p.id}`"
          class="flex items-start justify-between gap-4 border-b pb-3 last:border-0"
        >
          <div class="min-w-0">
            <div class="flex items-center gap-2 text-sm">
              <span class="font-mono font-medium">{{ p.id }}</span>
              <Badge variant="default">{{ p.level }}</Badge>
              <span v-if="scope(p)" class="font-mono text-fg-muted">{{ scope(p) }}</span>
              <Badge v-if="shipped.includes(p.id)" variant="ok">shipped</Badge>
            </div>
            <pre class="mt-1 overflow-x-auto text-xs text-fg-muted">{{ p.text }}</pre>
          </div>
          <div class="flex shrink-0 gap-2">
            <Button size="sm" variant="outline" @click="edit(p)">Edit</Button>
            <Button size="sm" variant="outline" @click="remove(p)">Delete</Button>
          </div>
        </div>
        <Button v-if="!editing" class="self-start" @click="editing = true">New policy</Button>
      </CardContent>
    </Card>

    <Card v-if="editing">
      <CardHeader>
        <CardTitle>{{ draft.id ? draft.id : "New policy" }}</CardTitle>
      </CardHeader>
      <CardContent class="flex flex-col gap-3">
        <div class="flex flex-wrap gap-2">
          <Input v-model="draft.id" placeholder="policy id" class="w-56" />
          <Select v-model="draft.level">
            <SelectTrigger class="w-40"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="instance">instance</SelectItem>
              <SelectItem value="org">org</SelectItem>
              <SelectItem value="workspace">workspace</SelectItem>
              <SelectItem value="object">object</SelectItem>
            </SelectContent>
          </Select>
          <Input
            v-if="draft.level === 'workspace'"
            v-model="draft.workspace_id"
            placeholder="workspace id"
            class="w-48"
          />
          <template v-if="draft.level === 'object'">
            <Input v-model="draft.object_kind" placeholder="kind (secret, source...)" class="w-48" />
            <Input v-model="draft.object_id" placeholder="object id" class="w-48" />
          </template>
        </div>
        <Textarea
          v-model="draft.text"
          class="min-h-40 font-mono text-xs"
          placeholder='permit(principal, action == Archie::Action::"read", resource);'
        />
        <div class="flex gap-2">
          <Button :disabled="!canSave || saving" @click="save">Save</Button>
          <Button variant="outline" @click="(editing = false), (draft = blank())">Cancel</Button>
        </div>
      </CardContent>
    </Card>
  </div>
</template>
