<script setup lang="ts">
import { RefreshCw } from "@lucide/vue";
import { onMounted, ref } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { api } from "@/lib/api";
import StartWorkForm from "./StartWorkForm.vue";
import type { StageStats } from "./stages";
import StagesSection from "./StagesSection.vue";
import WorkflowTable from "./WorkflowTable.vue";
import type { WorkflowDefinition, WorkflowStats } from "./workflow-rows";

/** Run outcomes and spend per workflow, and where stages get stuck. */

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

onMounted(load);
</script>

<template>
  <div>
    <PageHeader title="Workflows" subtitle="Run outcomes and spend per workflow, and where stages get stuck.">
      <Button variant="outline" @click="load">
        <RefreshCw data-icon="inline-start" />
        Refresh
      </Button>
    </PageHeader>

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
