<script setup lang="ts">
import { computed } from "vue";

import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import ConfigCard from "./ConfigCard.vue";
import RepoBoolCell from "./RepoBoolCell.vue";
import RepoIntCell from "./RepoIntCell.vue";
import { config, configEditable } from "./state";

/**
 * Each repository Archie polls, and the quality gate a change must pass before
 * it opens a pull request.
 *
 * Concurrent tasks, retries and self-review are per-repository overrides,
 * written through PATCH /api/config/repos/{owner}/{name} rather than into
 * config.toml directly, so a change here takes effect without editing a file
 * on the host. A process with no write path renders those three as values: its
 * PATCH route answers 503 (archie-core-ymut).
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
  <ConfigCard v-if="!repos.length" title="Repositories" description="The repositories Archie polls for work.">
    <Empty>
      <EmptyHeader>
        <EmptyTitle>No repositories configured</EmptyTitle>
        <EmptyDescription>Add a [[repos]] entry in config.toml so Archie has somewhere to work.</EmptyDescription>
      </EmptyHeader>
    </Empty>
  </ConfigCard>
  <ConfigCard
    v-else
    title="Repositories"
    description="Each repository Archie polls, and the quality gate a change must pass before it opens a pull request. Concurrent tasks, retries, and self-review are per-repository overrides -- PATCH /api/config/repos/{owner}/{name}, not the file config.toml directly."
  >
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
          <TableCell class="font-medium">{{ repo.owner }}/{{ repo.name }}</TableCell>
          <TableCell class="font-mono">{{ repo.base }}</TableCell>
          <TableCell>{{ repo.ecosystem || "go" }}</TableCell>
          <TableCell class="font-mono">{{ gateSummary(repo.gate) }}</TableCell>
          <TableCell class="font-mono">{{ repo.protect?.length ? repo.protect.join(", ") : "—" }}</TableCell>
          <RepoBoolCell :repo="repo" field="allow_concurrent" :editable="configEditable" />
          <RepoIntCell :repo="repo" field="max_retries" :editable="configEditable" />
          <RepoBoolCell :repo="repo" field="review_enabled" :editable="configEditable" />
        </TableRow>
      </TableBody>
    </Table>
  </ConfigCard>
</template>
