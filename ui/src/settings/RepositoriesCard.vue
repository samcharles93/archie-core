<script setup lang="ts">
import { computed } from "vue";

import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import ConfigCard from "./ConfigCard.vue";
import { config } from "./state";

/**
 * Each repository Archie polls, and the quality gate a change must pass before
 * it opens a pull request.
 *
 * Concurrent tasks, retries and self-review are per-repository policy. They
 * are edited as the repository-policies control-plane resource, so this table
 * reports what each repository is running with.
 */
const repos = computed(() => config.value?.repositories ?? []);

/** The gate is shell argv lists, so it reads as the commands an operator would
 * type, joined by the arrow the pipeline follows. */
function gateSummary(gate?: string[][]): string {
  if (!gate?.length) return "—";
  return gate.map((cmd) => cmd.join(" ")).join("  →  ");
}
</script>

<template>
  <ConfigCard v-if="!repos.length">
    <Empty>
      <EmptyHeader>
        <EmptyTitle>No repositories configured</EmptyTitle>
        <EmptyDescription
          >Add a [[repos]] entry in config.toml so Archie has somewhere to
          work.</EmptyDescription
        >
      </EmptyHeader>
    </Empty>
  </ConfigCard>
  <ConfigCard v-else>
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Repository</TableHead>
          <TableHead>Base branch</TableHead>
          <TableHead>Ecosystem</TableHead>
          <TableHead>Quality gate</TableHead>
          <TableHead>Protected paths</TableHead>
          <TableHead>Concurrent</TableHead>
          <TableHead>Max retries</TableHead>
          <TableHead>Self-review</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(repo, index) in repos" :key="index">
          <TableCell class="font-medium"
            >{{ repo.owner }}/{{ repo.name }}</TableCell
          >
          <TableCell class="font-mono">{{ repo.base }}</TableCell>
          <TableCell>{{ repo.ecosystem || "go" }}</TableCell>
          <TableCell class="font-mono">{{ gateSummary(repo.gate) }}</TableCell>
          <TableCell class="font-mono">{{
            repo.protect?.length ? repo.protect.join(", ") : "—"
          }}</TableCell>
          <TableCell>{{ repo.allow_concurrent ? "yes" : "no" }}</TableCell>
          <TableCell class="font-mono">{{ repo.max_retries ?? 0 }}</TableCell>
          <TableCell>{{ repo.review_enabled ? "yes" : "no" }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </ConfigCard>
</template>
