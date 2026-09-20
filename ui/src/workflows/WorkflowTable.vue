<script setup lang="ts">
import { computed } from "vue";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { pct, workflowLabel } from "./labels";
import WorkflowBar from "./WorkflowBar.vue";
import { workflowRows, type WorkflowDefinition, type WorkflowStats } from "./workflow-rows";

/** Run outcomes per workflow, definitions merged with whatever stats exist. */
const props = defineProps<{ workflows: WorkflowStats[]; definitions: WorkflowDefinition[] }>();

const rows = computed(() => workflowRows(props.workflows, props.definitions));
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Per workflow</CardTitle>
      <CardDescription v-if="rows.length">Success rate is merged tasks over all runs</CardDescription>
    </CardHeader>
    <CardContent>
      <Empty v-if="!rows.length">
        <EmptyHeader>
          <EmptyTitle>No workflow runs yet</EmptyTitle>
          <EmptyDescription>Stats appear here once a task has run through a workflow.</EmptyDescription>
        </EmptyHeader>
      </Empty>
      <Table v-else>
        <TableHeader>
          <TableRow>
            <TableHead>Workflow</TableHead>
            <TableHead>Origin</TableHead>
            <TableHead>Runs</TableHead>
            <TableHead>Success rate</TableHead>
            <TableHead>Avg tokens</TableHead>
            <TableHead>Avg steps</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="w in rows" :key="w.workflow">
            <TableCell class="font-medium">{{ workflowLabel(w.workflow) }}</TableCell>
            <TableCell class="font-mono">{{ w.origin || "registry" }}</TableCell>
            <TableCell>{{ `${w.runs} run${w.runs === 1 ? "" : "s"}` }}</TableCell>
            <TableCell>
              <div class="flex items-baseline gap-2 text-sm">
                {{ pct(w.merged, w.runs) }}%
                <span class="text-xs text-fg-subtle">{{ w.merged }} of {{ w.runs }} merged</span>
              </div>
              <WorkflowBar :fraction="pct(w.merged, w.runs)" :kind="pct(w.merged, w.runs) >= 50 ? 'ok' : 'warn'" />
            </TableCell>
            <TableCell class="font-mono">{{ w.avg_tokens ? w.avg_tokens.toLocaleString() : "—" }}</TableCell>
            <TableCell class="font-mono">{{ w.avg_steps ? w.avg_steps.toFixed(1) : "—" }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </CardContent>
  </Card>
</template>
