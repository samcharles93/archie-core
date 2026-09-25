<script setup lang="ts">
import { computed, onMounted, ref, watchEffect } from "vue";
import { Plus, RotateCcw } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { StatusPill } from "@/components/ui/status-pill";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import { compact } from "@/lib/format";
import { cn } from "@/lib/utils";
import { statusLabel } from "@/lib/task-meta";
import { cloneControlPlaneValue, useControlPlaneStore, type WorkflowDefinitionCollection } from "@/stores/control-plane";
import { useLiveResource } from "@/stores/live-updates";
import SlowestStagesCard from "./SlowestStagesCard.vue";
import StageFailuresCard from "./StageFailuresCard.vue";
import type { StageStats } from "./stages";
import WorkflowEditor from "./WorkflowEditor.vue";
import { workflowRows, type WorkflowDefinition, type WorkflowStats } from "./workflow-rows";

interface WorkflowsResponse {
  definitions?: WorkflowDefinition[];
  workflows?: WorkflowStats[];
  stages?: StageStats[];
}
interface RunTask {
  id: number | string;
  title?: string;
  status?: string;
  workflow?: string;
  owner?: string;
  repo?: string;
  issue_number?: number;
}

const store = useControlPlaneStore();
const data = ref<WorkflowsResponse>({});
const runs = ref<RunTask[]>([]);
const error = ref<string | null>(null);

async function load() {
  try {
    const [workflows, tasks] = await Promise.all([api.workflows<WorkflowsResponse>(), api.tasks<RunTask[]>()]);
    data.value = workflows ?? {};
    runs.value = tasks ?? [];
    error.value = null;
  } catch (err) {
    error.value = String((err as Error).message || err);
  }
}
useLiveResource("tasks", () => void load(), 500);
onMounted(() => Promise.all([load(), store.load()]));

// Stored definitions are the source of truth for the list; statistics join
// them by id, and a workflow seen only in statistics still gets a row.
const stored = computed(
  () => (store.stateFor("workflow-definitions").resource?.value as WorkflowDefinitionCollection | undefined)?.definitions ?? [],
);
const rows = computed(() =>
  workflowRows(
    data.value.workflows,
    stored.value.map((d) => ({
      id: d.id,
      name: d.id,
      origin: data.value.definitions?.find((x) => x.id === d.id)?.origin,
    })),
  ),
);
const selected = ref("");
watchEffect(() => {
  if (!selected.value && rows.value.length) selected.value = rows.value[0]!.id;
});
const current = computed(() => rows.value.find((r) => r.id === selected.value));
const stages = computed(() => (data.value.stages ?? []).filter((s) => s.workflow === selected.value));
const workflowRuns = computed(() => runs.value.filter((t) => t.workflow === selected.value));
const tab = ref("definition");

function create() {
  let n = 1;
  while (stored.value.some((d) => d.id === `workflow-${n}`)) n++;
  selected.value = `workflow-${n}`;
  tab.value = "definition";
}
async function restoreShipped() {
  const shipped = store.shippedWorkflows();
  if (await store.replace("workflow-definitions", cloneControlPlaneValue(shipped)))
    selected.value = shipped.definitions[0]?.id ?? "";
}
const rate = (merged = 0, total = 0) => (total ? merged / total : 0);
</script>

<template>
  <div>
    <PageHeader title="Workflows">
      <Button variant="ghost" size="sm" :disabled="!store.shippedWorkflows().definitions.length" @click="restoreShipped">
        <RotateCcw data-icon="inline-start" /> Restore shipped
      </Button>
      <Button size="sm" @click="create"><Plus data-icon="inline-start" /> New workflow</Button>
    </PageHeader>
    <p class="-mt-4 mb-6 text-sm text-fg-muted">How work runs. Success is merged tasks over all runs.</p>

    <p v-if="error" class="mb-4 text-sm text-danger" role="alert">Cannot reach archied: {{ error }}</p>

    <div class="grid items-start gap-6 lg:grid-cols-[24rem_minmax(0,1fr)]">
      <ul class="flex flex-col gap-0.5" aria-label="Workflows">
        <li v-for="row in rows" :key="row.id">
          <button
            type="button"
            :aria-current="row.id === selected ? 'true' : undefined"
            :class="cn('w-full rounded-md px-3 py-2.5 text-left transition-colors hover:bg-secondary', row.id === selected && 'bg-secondary')"
            @click="selected = row.id"
          >
            <span class="flex items-baseline gap-2">
              <span class="min-w-0 flex-1 truncate font-mono text-[13px]" :class="row.id === selected && 'font-medium'">{{ row.id }}</span>
              <span class="font-mono text-xs text-fg-subtle">{{ row.avg_tokens ? compact(row.avg_tokens) : "–" }}</span>
            </span>
            <span class="mt-1.5 flex items-center gap-2 text-xs text-fg-subtle">
              <span class="w-20 shrink-0">{{ row.runs || 0 }} runs</span>
              <span class="h-1 flex-1 overflow-hidden rounded-full bg-secondary">
                <span class="block h-full rounded-full bg-primary" :style="{ width: `${rate(row.merged, row.runs) * 100}%` }" />
              </span>
              <span class="w-16 shrink-0 text-right font-mono">{{ row.merged || 0 }} of {{ row.runs || 0 }}</span>
            </span>
          </button>
        </li>
      </ul>

      <section class="min-w-0 rounded-lg border border-border bg-card px-5 py-4" aria-label="Workflow">
        <header class="mb-2 flex flex-wrap items-center gap-2">
          <h2 class="font-mono text-[15px] font-medium">{{ selected || "No workflow" }}</h2>
          <StatusPill v-if="current?.origin">{{ current.origin }}</StatusPill>
          <span v-if="current" class="ml-auto text-xs text-fg-subtle">{{ current.runs || 0 }} runs</span>
        </header>
        <Tabs v-model="tab">
          <TabsList>
            <TabsTrigger value="definition">Definition</TabsTrigger>
            <TabsTrigger value="performance">Performance</TabsTrigger>
            <TabsTrigger value="runs">Runs <span class="ml-1 font-mono text-xs text-fg-subtle">{{ workflowRuns.length }}</span></TabsTrigger>
          </TabsList>
          <TabsContent value="definition" class="pt-4">
            <WorkflowEditor v-model:selected="selected" />
          </TabsContent>
          <TabsContent value="performance" class="grid gap-4 pt-4">
            <p v-if="!stages.length" class="text-sm text-fg-muted">No stage timings recorded for this workflow yet.</p>
            <template v-else>
              <SlowestStagesCard :stages="stages" />
              <StageFailuresCard :stages="stages" />
            </template>
          </TabsContent>
          <TabsContent value="runs" class="pt-4">
            <p v-if="!workflowRuns.length" class="text-sm text-fg-muted">No tasks have run this workflow.</p>
            <ul v-else class="divide-y divide-border">
              <li v-for="t in workflowRuns" :key="t.id" class="flex items-center gap-3 py-2 text-sm">
                <RouterLink :to="`/tasks/${t.id}`" class="min-w-0 flex-1 truncate hover:underline">{{ t.title || `Task ${t.id}` }}</RouterLink>
                <span class="font-mono text-xs text-fg-subtle">{{ t.owner && t.repo ? `${t.owner}/${t.repo} #${t.issue_number}` : "" }}</span>
                <StatusPill>{{ statusLabel(t.status ?? "") }}</StatusPill>
              </li>
            </ul>
          </TabsContent>
        </Tabs>
      </section>
    </div>
  </div>
</template>
