<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ago } from "@/lib/format";
import {
  useControlPlaneStore,
  type AuditEntry,
  type ResourceDescriptor,
} from "@/stores/control-plane";

/**
 * The field-level audit of the settings a page shows: one row per changed
 * field, newest first. Restore puts a resource back to the version before a
 * change, as an ordinary replace, so the restore is audited like any edit.
 */
const props = defineProps<{ resources: ResourceDescriptor[] }>();

const store = useControlPlaneStore();
const entries = ref<AuditEntry[]>([]);
const error = ref("");

const kinds = computed(() => props.resources.map((r) => r.kind));
const titles = computed(
  () => new Map(props.resources.map((r) => [r.kind, r.title])),
);
// Every stored version on the page: a save anywhere refreshes the list.
const versions = computed(() =>
  kinds.value.map((kind) => store.stateFor(kind).resource?.version).join(","),
);

async function load(): Promise<void> {
  if (!kinds.value.length) return;
  try {
    entries.value = await store.audit("resources", kinds.value);
    error.value = "";
  } catch (err) {
    error.value = String((err as Error).message || err);
  }
}
watch([kinds, versions], load, { immediate: true });

function show(value: unknown): string {
  return value === null || value === undefined ? "" : JSON.stringify(value);
}

async function restoreBefore(entry: AuditEntry): Promise<void> {
  const previous = (await store.history(entry.record_key)).find(
    (revision) => revision.version === entry.version - 1,
  );
  if (previous) await store.replace(entry.record_key, previous.value);
}
</script>

<template>
  <Card class="mb-4">
    <CardHeader><CardTitle>Audit</CardTitle></CardHeader>
    <CardContent>
    <p v-if="error" class="text-sm text-destructive" role="alert">
      {{ error }}
    </p>
    <Table v-else>
      <TableHeader>
        <TableRow>
          <TableHead>When</TableHead>
          <TableHead>Setting</TableHead>
          <TableHead>Field</TableHead>
          <TableHead>Old value</TableHead>
          <TableHead>New value</TableHead>
          <TableHead>Version</TableHead>
          <TableHead>Changed by</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="entry in entries" :key="entry.id">
          <TableCell class="whitespace-nowrap" :title="entry.at">{{
            entry.at ? ago(entry.at) : ""
          }}</TableCell>
          <TableCell>{{
            titles.get(entry.record_key) || entry.record_key
          }}</TableCell>
          <TableCell class="font-mono text-xs">{{
            entry.field || "(whole value)"
          }}</TableCell>
          <TableCell
            class="max-w-64 truncate font-mono text-xs text-fg-muted"
            :title="show(entry.old_value)"
            >{{ show(entry.old_value) }}</TableCell
          >
          <TableCell
            class="max-w-64 truncate font-mono text-xs"
            :title="show(entry.new_value)"
            >{{ show(entry.new_value) }}</TableCell
          >
          <TableCell>{{ entry.version }}</TableCell>
          <TableCell class="whitespace-nowrap"
            >{{ entry.actor
            }}<span class="text-fg-muted"> · {{ entry.source }}</span></TableCell
          >
          <TableCell>
            <Button
              v-if="entry.version > 1"
              variant="ghost"
              size="sm"
              :disabled="store.stateFor(entry.record_key).saving"
              @click="restoreBefore(entry)"
              >Restore</Button
            >
          </TableCell>
        </TableRow>
        <TableEmpty v-if="!entries.length" :colspan="8"
          >No changes recorded yet.</TableEmpty
        >
      </TableBody>
    </Table>
    </CardContent>
  </Card>
</template>
