<script setup lang="ts">
import { Card, CardContent } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import MappingRow from "./MappingRow.vue";
import { listLoading, loadError, mappings, requestDelete, startEdit } from "./state";

/**
 * The saved mappings, in all four states: still loading, unreadable, none yet,
 * and the table. A list that cannot be read says so instead of rendering empty,
 * because "no mappings yet" and "cannot reach archied" call for different
 * actions from whoever is looking.
 */
</script>

<template>
  <div v-if="listLoading" class="flex flex-col gap-2" aria-busy="true">
    <Skeleton class="h-10 w-full" />
    <Skeleton class="h-28 w-full" />
  </div>

  <Empty v-else-if="loadError">
    <EmptyHeader>
      <EmptyTitle>Cannot reach archied</EmptyTitle>
      <EmptyDescription>{{ loadError }}</EmptyDescription>
    </EmptyHeader>
  </Empty>

  <Empty v-else-if="!mappings.length">
    <EmptyHeader>
      <EmptyTitle>No mappings yet</EmptyTitle>
      <EmptyDescription>Create one from a captured event to reuse its fields in a future playbook binding.</EmptyDescription>
    </EmptyHeader>
  </Empty>

  <Card v-else>
    <CardContent>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Event type</TableHead>
            <TableHead>Fields</TableHead>
            <TableHead>Matched</TableHead>
            <TableHead>Last match</TableHead>
            <TableHead><span class="sr-only">Actions</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <MappingRow
            v-for="mapping in mappings"
            :key="mapping.id"
            :mapping="mapping"
            @edit="startEdit"
            @delete="requestDelete"
          />
        </TableBody>
      </Table>
    </CardContent>
  </Card>
</template>
