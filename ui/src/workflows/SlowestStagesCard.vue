<script setup lang="ts">
import { computed } from "vue";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatMs, pct, workflowLabel } from "./labels";
import { maxStageMs, slowestStages, type StageStats } from "./stages";
import WorkflowBar from "./WorkflowBar.vue";

/** Which stage is eating the time. */
const props = defineProps<{ stages: StageStats[] }>();

const rows = computed(() => slowestStages(props.stages));
const maxMs = computed(() => maxStageMs(props.stages));
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Slowest stages</CardTitle>
      <CardDescription>Average duration, this workflow's stages</CardDescription>
    </CardHeader>
    <CardContent>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Workflow</TableHead>
            <TableHead>Stage</TableHead>
            <TableHead>Runs</TableHead>
            <TableHead>Avg duration</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(s, idx) in rows" :key="idx">
            <TableCell>{{ workflowLabel(s.workflow) }}</TableCell>
            <TableCell class="font-medium">{{ s.stage }}</TableCell>
            <TableCell>{{ s.runs }}</TableCell>
            <TableCell>
              <div class="text-sm">{{ formatMs(s.avg_ms) }}</div>
              <WorkflowBar :fraction="pct(s.avg_ms || 0, maxMs)" kind="info" />
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </CardContent>
  </Card>
</template>
