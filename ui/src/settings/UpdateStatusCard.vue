<script setup lang="ts">
import ConfigCard from "./ConfigCard.vue";
import { Card, CardContent } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { StatusKind } from "@/lib/status";
import { versionComponents, versionError, versionMissing } from "./state";

/**
 * How each component is deployed, and whether what is running matches what is
 * installed. The comparison is read-only; what to do about an available update
 * is UpdateActionsCard's job.
 */

const STATUS_LABELS: Record<string, string> = {
  ok: "OK",
  update_available: "Update available",
  drift: "Drift",
  unknown: "Unknown",
};
const STATUS_KINDS: Record<string, StatusKind> = {
  ok: "ok",
  update_available: "info",
  drift: "danger",
  unknown: "idle",
};

/** The exact versions compared, shown on the row that compares them. */
function basis(component: (typeof versionComponents.value)[number]): string {
  return [
    `Installed claim: ${component.installed_claim || "unknown"}`,
    `Running: ${component.running_version || "not observed"}`,
    component.reference ? `Reference: ${component.reference}` : null,
  ]
    .filter(Boolean)
    .join("\n");
}
</script>

<template>
  <!-- A 501 is a deployment that did not wire update checking, which is a
       legitimate state and a different thing from a broken one. Saying so once
       here covers the absent UpdateActionsCard too. -->
  <ConfigCard
    v-if="versionMissing"
    title="Update status"
    description="Not configured on this deployment."
  />
  <Card v-else-if="versionError" class="mb-4">
    <CardContent>
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Update status unavailable</EmptyTitle>
          <EmptyDescription>{{ versionError }}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    </CardContent>
  </Card>
  <ConfigCard
    v-else
    title="Update status"
    description="Whether what's running matches what's installed."
  >
    <Empty v-if="!versionComponents.length">
      <EmptyHeader>
        <EmptyTitle>No components reported</EmptyTitle>
        <EmptyDescription>The configured update-check command returned nothing.</EmptyDescription>
      </EmptyHeader>
    </Empty>
    <Table v-else>
      <TableHeader>
        <TableRow>
          <TableHead>Component</TableHead>
          <TableHead>Install type</TableHead>
          <TableHead>Running</TableHead>
          <TableHead>Latest available</TableHead>
          <TableHead>Status</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="component in versionComponents" :key="component.id || component.label" :title="basis(component)">
          <TableCell class="font-medium">{{ component.label || component.id }}</TableCell>
          <TableCell class="font-mono">{{ component.install_type || "—" }}</TableCell>
          <TableCell class="font-mono">{{ component.running_version || "—" }}</TableCell>
          <TableCell class="font-mono">{{ component.latest_available || "—" }}</TableCell>
          <TableCell>
            <Badge :variant="STATUS_KINDS[component.status || ''] || 'idle'">
              {{ STATUS_LABELS[component.status || ""] || component.status }}
            </Badge>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </ConfigCard>
</template>
