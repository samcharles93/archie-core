<script setup lang="ts">
import { onMounted, ref } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { Card, CardContent } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
import StartWorkForm from "./StartWorkForm.vue";
import type { StageStats } from "./stages";
import StagesSection from "./StagesSection.vue";
import WorkflowTable from "./WorkflowTable.vue";
import type { WorkflowDefinition, WorkflowStats } from "./workflow-rows";

interface WorkflowsResponse {
  definitions?: WorkflowDefinition[];
  workflows?: WorkflowStats[];
  stages?: StageStats[];
}

const definitions = ref<WorkflowDefinition[]>([]);
const workflows = ref<WorkflowStats[]>([]);
const stages = ref<StageStats[]>([]);
const error = ref<string | null>(null);

async function load() {
  try {
    const data = await api.workflows<WorkflowsResponse>();
    definitions.value = data?.definitions || [];
    workflows.value = data?.workflows || [];
    stages.value = data?.stages || [];
    error.value = null;
  } catch (err) {
    error.value = String((err as Error).message || err);
  }
}

useLiveResource("tasks", () => void load(), 500);
onMounted(load);
</script>

<template>
  <div>
    <PageHeader title="Workflows" />

    <!-- Starting work needs a definition to start: with none served, the form
         could only be submitted to fail. -->
    <StartWorkForm v-if="definitions.length" :definitions="definitions" />

    <Card v-if="error">
      <CardContent>
        <Empty>
          <EmptyHeader>
            <EmptyTitle>Cannot reach archied</EmptyTitle>
            <EmptyDescription>{{ error }}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </CardContent>
    </Card>
    <template v-else>
      <WorkflowTable :workflows="workflows" :definitions="definitions" />
      <StagesSection :stages="stages" />
    </template>
  </div>
</template>
