<script setup lang="ts">
import { computed } from "vue";

import { Card, CardContent } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import BindingRow from "./BindingRow.vue";
import type { Binding } from "./binding-draft";
import type { LoadFailure } from "./use-bindings";

const props = defineProps<{
  /** null means loading, [] means loaded and empty. */
  bindings: Binding[] | null;
  failure: LoadFailure | null;
}>();

const emit = defineEmits<{
  edit: [binding: Binding];
  approve: [binding: Binding];
  delete: [binding: Binding];
}>();

const loading = computed(() => props.bindings === null);
const rows = computed(() => props.bindings ?? []);

// The failure description is the daemon's own words. "bindings not configured"
// answers a deployment with no binding store wired, and the operator can act on
// that, where a blanket "cannot reach archied" would be false about a daemon
// that answered.
const failureTitle = computed(() =>
  props.failure?.kind === "unconfigured" ? "Bindings are not configured" : "Cannot reach archied",
);
</script>

<template>
  <Empty v-if="props.failure">
    <EmptyHeader>
      <EmptyTitle>{{ failureTitle }}</EmptyTitle>
      <EmptyDescription>{{ props.failure.message }}</EmptyDescription>
    </EmptyHeader>
  </Empty>

  <Empty v-else-if="!loading && !rows.length">
    <EmptyHeader>
      <EmptyTitle>No bindings yet</EmptyTitle>
      <EmptyDescription>
        Create one from a saved field mapping to turn a captured webhook into a workflow.
      </EmptyDescription>
    </EmptyHeader>
  </Empty>

  <Card v-else>
    <CardContent>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Source</TableHead>
            <TableHead>Workflow</TableHead>
            <TableHead>Repo pin</TableHead>
            <TableHead>Status</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-if="loading">
            <TableRow v-for="row in 3" :key="row">
              <TableCell v-for="col in 6" :key="col"><Skeleton class="h-4 w-full" /></TableCell>
            </TableRow>
          </template>
          <template v-else>
            <BindingRow
              v-for="binding in rows"
              :key="binding.id"
              :binding="binding"
              @edit="emit('edit', $event)"
              @approve="emit('approve', $event)"
              @delete="emit('delete', $event)"
            />
          </template>
        </TableBody>
      </Table>
    </CardContent>
  </Card>
</template>
