<script setup lang="ts">
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { config } from "./state";

/**
 * The LLM providers wired up, and whether their credentials are actually
 * present. Only the environment variable NAME is shown, never its value; a
 * provider with no credential form set is not an error, just unusable, which
 * is what the status column says.
 */
const providers = computed(() => Object.entries(config.value?.providers ?? {}));
</script>

<template>
  <h3 class="mt-5 mb-2 text-sm font-semibold text-fg-muted">Providers</h3>
  <Empty v-if="!providers.length">
    <EmptyHeader>
      <EmptyTitle>No providers configured</EmptyTitle>
      <EmptyDescription>Add a [providers.&lt;name&gt;] entry so a model role above has something to run on.</EmptyDescription>
    </EmptyHeader>
  </Empty>
  <Table v-else>
    <TableHeader>
      <TableRow>
        <TableHead>Provider</TableHead>
        <TableHead>Class</TableHead>
        <TableHead>Base URL</TableHead>
        <TableHead>API key env var</TableHead>
        <TableHead>Status</TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      <TableRow v-for="[name, provider] in providers" :key="name">
        <TableCell class="font-medium">{{ name }}</TableCell>
        <TableCell>{{ provider.class }}</TableCell>
        <TableCell class="font-mono">{{ provider.base_url || "default" }}</TableCell>
        <TableCell class="font-mono">{{ provider.api_key_env || "—" }}</TableCell>
        <TableCell>
          <Badge v-if="provider.configured" variant="ok">configured</Badge>
          <Badge v-else variant="warn">missing credentials</Badge>
        </TableCell>
      </TableRow>
    </TableBody>
  </Table>
</template>
