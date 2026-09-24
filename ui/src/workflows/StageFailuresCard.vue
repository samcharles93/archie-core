<script setup lang="ts">
import { computed } from "vue";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
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
import { pct, workflowLabel } from "./labels";
import { failingStages, type StageStats } from "./stages";
import WorkflowBar from "./WorkflowBar.vue";

/** Which stage is failing, over how many runs. */
const props = defineProps<{ stages: StageStats[] }>();

const rows = computed(() => failingStages(props.stages));
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Most failures</CardTitle>
      <CardDescription>Stages that error out, over their runs</CardDescription>
    </CardHeader>
    <CardContent>
      <Empty v-if="!rows.length">
        <EmptyHeader>
          <EmptyTitle>No failures recorded</EmptyTitle>
          <EmptyDescription
            >Every stage has completed cleanly so far.</EmptyDescription
          >
        </EmptyHeader>
      </Empty>
      <Table v-else>
        <TableHeader>
          <TableRow>
            <TableHead>Workflow</TableHead>
            <TableHead>Stage</TableHead>
            <TableHead>Failures</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(s, idx) in rows" :key="idx">
            <TableCell>{{ workflowLabel(s.workflow) }}</TableCell>
            <TableCell class="font-medium">{{ s.stage }}</TableCell>
            <TableCell>
              <div class="text-sm">{{ s.errors }} of {{ s.runs }}</div>
              <WorkflowBar :fraction="pct(s.errors, s.runs)" kind="danger" />
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </CardContent>
  </Card>
</template>
