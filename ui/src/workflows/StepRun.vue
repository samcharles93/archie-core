<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
import { Badge } from "@/components/ui/badge";
import ChangeCapture from "@/tasks/ChangeCapture.vue";
import TaskLogs from "@/tasks/TaskLogs.vue";
import { stageStatusMeta } from "@/tasks/stage-rail";
import { describeTimelineEvent, duration } from "@/tasks/timeline-event";
import type { ChangesState, LogState, TaskEvent, TaskRecord } from "@/tasks/task-run";
import type { StageRun } from "./workflow-graph";

const props = defineProps<{ task: TaskRecord; attempt: number; stage: StageRun; stages: StageRun[] }>();
const events = ref<TaskEvent[]>([]);
const changes = ref<ChangesState>();
const logs = ref<LogState | null>();
const activityError = ref("");
const changesError = ref("");
const expanded = ref(false);
const key = computed(() => `${props.task.id}/${props.attempt}/${props.stage.id ?? props.stage.name}`);
let generation = 0;
let timer: ReturnType<typeof setInterval> | undefined;
let loading = false;
let queued = false;

async function refresh(): Promise<void> {
  if (!props.attempt) return;
  if (loading) { queued = true; return; }
  loading = true;
  const current = generation;
  const id = String(props.task.id);
  const params = { attempt: props.attempt };
  const results = await Promise.allSettled([
    api.taskDebug<{ events?: TaskEvent[] }>(id, params),
    api.taskChanges<ChangesState>(id, params),
    api.taskLogs<LogState>(id, { ...params, stage: props.stage.name }),
  ]);
  if (current !== generation) return;
  const [activity, capture, log] = results;
  activityError.value = activity.status === "rejected" ? "Could not load step activity" : "";
  if (activity.status === "fulfilled") events.value = activity.value.events ?? [];
  changesError.value = capture.status === "rejected" ? "Could not load captured changes" : "";
  if (capture.status === "fulfilled") changes.value = capture.value;
  logs.value = log.status === "fulfilled" ? log.value : null;
  loading = false;
  if (queued) { queued = false; void refresh(); }
}

watch(key, () => {
  generation++;
  loading = false;
  queued = false;
  events.value = [];
  changes.value = undefined;
  logs.value = undefined;
  activityError.value = "";
  changesError.value = "";
  expanded.value = false;
  void refresh();
}, { immediate: true });
watch(() => props.stage.status, (status) => {
  clearInterval(timer);
  void refresh();
  // Logs can arrive between task events; read them while the stage is active.
  if (status === "running") timer = setInterval(() => void refresh(), 2000);
}, { immediate: true });
useLiveResource("tasks", () => void refresh(), 300);
onUnmounted(() => { generation++; clearInterval(timer); });

const meta = computed(() => stageStatusMeta(props.stage.status));
const names = computed(() => new Set([props.stage.name, ...(props.stage.agents ?? []).map((agent) => agent.name.split("/").at(-1) ?? agent.name)]));
const nextStart = computed(() => props.stages.find((stage) => stage.name === props.stage.name && stage.started_at && props.stage.started_at && stage.started_at > props.stage.started_at)?.started_at);
function inWindow(at: string | number | Date | undefined): boolean {
  if (!props.stage.started_at || !at) return props.stages.filter((stage) => stage.name === props.stage.name).length === 1;
  const time = new Date(at).getTime();
  return time >= Date.parse(props.stage.started_at) && (!props.stage.finished_at || time <= Date.parse(props.stage.finished_at)) && (!nextStart.value || time < Date.parse(nextStart.value));
}
const activity = computed(() => events.value.filter((event) => {
  if (Number(event.attempt) !== props.attempt) return false;
  if (event.data?.step_id != null) return Number(event.data.step_id) === props.stage.id;
  return names.value.has(event.stage ?? "") && inWindow(event.at);
}).sort((a, b) => String(a.at ?? "").localeCompare(String(b.at ?? ""))));
const groups = computed(() => {
  const agents = props.stage.agents ?? [];
  const branches = new Set(["", ...agents.map((agent) => agent.name.includes("/") ? agent.name.split("/")[0]! : ""), ...activity.value.map((event) => String(event.data?.branch ?? ""))]);
  return [...branches].map((branch) => ({
    branch,
    agents: agents.filter((agent) => (agent.name.includes("/") ? agent.name.split("/")[0] : "") === branch),
    reports: activity.value.filter((event) => event.kind === "agent_finish" && String(event.data?.branch ?? "") === branch),
    tools: activity.value.filter((event) => event.kind === "tool_call" && String(event.data?.branch ?? "") === branch),
  })).filter((group) => group.agents.length || group.reports.length || group.tools.length);
});
const captures = computed(() => (changes.value?.captures ?? []).filter((capture) => capture.stage === props.stage.name && inWindow(capture.captured_at)));
const scopedLogs = computed(() => logs.value ? { ...logs.value, entries: (logs.value.entries ?? []).filter((entry) => inWindow(entry.time)) } : logs.value);
</script>

<template>
  <div class="space-y-5 p-4 text-sm">
    <div class="flex items-center gap-2">
      <Badge :variant="meta.kind">{{ meta.label }}</Badge>
      <span class="text-xs text-muted-foreground">{{ stage.duration_ms == null ? "Duration not recorded" : duration(stage.duration_ms) }}</span>
      <span class="ml-auto text-xs text-muted-foreground">Attempt {{ attempt }}</span>
    </div>
    <p v-if="stage.error" class="rounded-md bg-danger-soft p-3 break-words whitespace-pre-wrap text-danger">{{ stage.error }}</p>
    <p v-if="activityError" role="alert" class="text-danger">{{ activityError }}</p>
    <section v-for="group in groups" :key="group.branch" class="space-y-3">
      <h3 v-if="group.branch" class="font-medium">{{ group.branch }}</h3>
      <p v-else-if="groups.length > 1" class="text-xs text-muted-foreground">Activity without branch attribution</p>
      <div v-for="agent in group.agents" :key="agent.id" class="space-y-1">
        <div class="flex items-center gap-2 text-xs"><span class="font-mono">{{ agent.name.split('/').at(-1) }}</span><Badge :variant="stageStatusMeta(agent.status).kind">{{ agent.status }}</Badge></div>
        <p v-if="agent.detail" class="break-words whitespace-pre-wrap">{{ agent.detail }}</p>
      </div>
      <div v-for="(report, i) in group.reports" :key="i" class="space-y-1">
        <p v-if="report.detail && !group.agents.some((agent) => agent.detail === report.detail)" class="break-words whitespace-pre-wrap">{{ report.detail }}</p>
        <p class="text-xs text-muted-foreground">Agent's own report · {{ describeTimelineEvent(report).title }}</p>
      </div>
      <div v-if="group.tools.length" class="space-y-1">
        <h4 class="text-xs font-medium text-muted-foreground">Tool calls · {{ group.tools.length }}</h4>
        <ol class="divide-y divide-border rounded-md border border-border">
          <li v-for="(tool, i) in expanded ? group.tools : group.tools.slice(0, 12)" :key="i" class="px-3 py-2">
            <div class="flex justify-between gap-2 text-xs"><span class="font-mono">{{ tool.data?.tool }}</span><span :class="tool.data?.failed ? 'text-danger' : 'text-muted-foreground'">{{ tool.data?.failed ? 'Failed' : 'Returned' }}</span></div>
            <p v-if="tool.detail" class="mt-1 break-words whitespace-pre-wrap text-xs text-muted-foreground">{{ tool.detail }}</p>
          </li>
        </ol>
        <button v-if="group.tools.length > 12" type="button" class="text-xs text-primary hover:underline" @click="expanded = !expanded">{{ expanded ? 'Show first 12' : `Show all ${group.tools.length}` }}</button>
      </div>
      <p v-if="group.reports.length" class="text-xs text-muted-foreground">{{ group.reports.map((report) => describeTimelineEvent(report).detail).filter(Boolean).join(' · ') }}</p>
      <p v-else-if="group.agents.length" class="text-xs text-muted-foreground">{{ group.agents.reduce((sum, agent) => sum + agent.tokens_used, 0).toLocaleString() }} tokens recorded</p>
    </section>
    <section class="space-y-3">
      <h3 class="text-xs font-medium text-muted-foreground">Captured changes</h3>
      <p v-if="changesError" role="alert" class="text-danger">{{ changesError }}</p>
      <ChangeCapture v-for="(capture, i) in captures" :key="i" :capture="capture" :task="task" />
      <p v-if="changes && !captures.length" class="text-xs text-muted-foreground">No changes captured for this step</p>
    </section>
    <section class="space-y-2">
      <h3 class="text-xs font-medium text-muted-foreground">Log <span title="Only lines tagged with this stage are shown">· stage-tagged lines</span></h3>
      <TaskLogs :state="scopedLogs" :attempt="attempt" :filterable="false" />
    </section>
  </div>
</template>
