<script setup lang="ts">
import { AlignLeft, Check, ListChecks, RefreshCw, Route, X } from "@lucide/vue";
import { computed, onMounted, onUnmounted, ref } from "vue";
import { RouterLink, useRouter } from "vue-router";

import Gauge from "@/base/Gauge.vue";
import SegmentBar from "@/base/SegmentBar.vue";
import StatTile from "@/base/StatTile.vue";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Progress } from "@/components/ui/progress";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, subscribeEvents } from "@/lib/api";
import { ago, compact } from "@/lib/format";
import type { StatusKind } from "@/lib/status";
import { activityDetail, type ActivityDetailInput } from "./activity-detail";
import { dismissSetupComplete, setupPanelState, type Setup } from "./setup-preference";
import { dashboardTaskTargets, type TaskTarget } from "./task-targets";

interface Summary {
  statuses?: Record<string, number>;
  tokens_by_day?: Array<{ tokens?: number }>;
}

interface WorkflowStat {
  workflow?: string;
  runs?: number;
  merged?: number;
  pr_open?: number;
  parked?: number;
}

/** An event off the live stream: the payload the detail column reads, plus
 * enough to point the row at the task it belongs to. */
interface ActivityEvent extends ActivityDetailInput {
  kind?: string;
  type?: string;
  task_id?: number | string;
  repo?: string;
  issue?: number | string;
  at?: string | number;
}

/**
 * The control room: what Archie is working on, what needs you, and what it is
 * spending.
 */

const activity = ref<ActivityEvent[]>([]);
const streamStateText = ref("connecting");
const streamStateKind = ref<StatusKind>("idle");
const taskIDsBySource = ref(new Map<string, number>());

const summary = ref<Summary | null>(null);
const setup = ref<Setup | null>(null);
const workflows = ref<{ workflows?: WorkflowStat[] } | null>(null);
// null means loading, [] means loaded.
const tasks = ref<TaskTarget[] | null>(null);
const error = ref<string | null>(null);

const router = useRouter();

// Bumped when the operator dismisses the setup card, so the panel state is
// re-evaluated; the preference itself lives in storage, not in the component.
const dismissals = ref(0);

onMounted(async () => {
  await load();
  unsubscribe = subscribeEvents(
    (event) => {
      const next = [event as ActivityEvent, ...activity.value];
      next.splice(50);
      activity.value = next;
    },
    (state) => {
      streamStateText.value = state;
      streamStateKind.value = state === "live" ? "ok" : "warn";
    },
  );
});

let unsubscribe: (() => void) | undefined;
onUnmounted(() => unsubscribe?.());

async function load() {
  error.value = null;
  try {
    const [nextSummary, nextSetup, nextWorkflows, nextTasks] = await Promise.all([
      api.summary<Summary>(),
      api.setup<Setup>().catch(() => null),
      api.workflows<{ workflows?: WorkflowStat[] }>().catch(() => null),
      api.tasks<TaskTarget[]>().catch(() => []),
    ]);
    const map = new Map<string, number>();
    for (const task of nextTasks as Array<TaskTarget & { owner?: string; repo?: string; issue_number?: number }>) {
      if (task.owner && task.repo && task.issue_number) {
        map.set(`${task.owner}/${task.repo}#${task.issue_number}`, Number(task.id));
      }
    }
    taskIDsBySource.value = map;
    summary.value = nextSummary;
    setup.value = nextSetup;
    workflows.value = nextWorkflows;
    tasks.value = nextTasks;
  } catch (err) {
    error.value = String((err as Error).message || err);
  }
}

function taskIDForEvent(event: ActivityEvent, bySource: Map<string, number>): number {
  if (Number(event.task_id) > 0) return Number(event.task_id);
  if (!event.repo || !event.issue) return 0;
  return Number(bySource.get(`${event.repo}#${event.issue}`)) || 0;
}

/** The task an activity row opens, or 0. An event carries the id, or a
 * repo/issue pair the task list resolves. */
const taskIDFor = (event: ActivityEvent): number => taskIDForEvent(event, taskIDsBySource.value);

function greeting(): string {
  const h = new Date().getHours();
  if (h < 5) return "Still up?";
  if (h < 12) return "Good morning";
  if (h < 18) return "Good afternoon";
  return "Good evening";
}

function trendPct(series: number[]): number | null {
  if (series.length < 4) return null;
  const mid = Math.floor(series.length / 2);
  const older = series.slice(0, mid).reduce((a, b) => a + b, 0);
  const newer = series.slice(mid).reduce((a, b) => a + b, 0);
  if (!older) return null;
  return ((newer - older) / older) * 100;
}

function openTask(taskID: number) {
  void router.push(`/tasks?task=${encodeURIComponent(taskID)}`);
}

function handleDismissSetup() {
  dismissSetupComplete();
  dismissals.value += 1;
}

const currentGreeting = computed(() => (setup.value?.operator ? `${greeting()}, ${setup.value.operator}` : greeting()));

const targets = computed(() => dashboardTaskTargets(tasks.value ?? []));

const setupPanel = computed(() => setupPanelState(setup.value));
const setupSteps = computed(() => setup.value?.steps ?? []);
const omitSetup = computed(() => setupPanel.value.kind === "omit" || setupPanel.value.kind === "dismissed");
const setupPct = computed(() => {
  const steps = setup.value?.steps ?? [];
  if (!steps.length) return 0;
  return Math.round(((steps.length - setupPanel.value.remaining.length) / steps.length) * 100);
});

const counts = computed(() => summary.value?.statuses ?? {});
const tokensByDay = computed(() => summary.value?.tokens_by_day ?? []);
const tokenSeries = computed(() => tokensByDay.value.map((d) => d.tokens || 0));

const workflowStats = computed(() => workflows.value?.workflows ?? []);
const runTotals = computed(() => {
  const runs = workflowStats.value.reduce((a, w) => a + (w.runs || 0), 0);
  const merged = workflowStats.value.reduce((a, w) => a + (w.merged || 0) + (w.pr_open || 0), 0);
  const parked = workflowStats.value.reduce((a, w) => a + (w.parked || 0), 0);
  return { runs, merged, parked };
});

const tokenTotals = computed(() => {
  const used = tokenSeries.value.reduce((a, b) => a + b, 0);
  const recent = tokenSeries.value.slice(-7).reduce((a, b) => a + b, 0);
  const perDay = tokensByDay.value.length ? used / tokensByDay.value.length : 0;
  return { used, recent, perDay, projected: Math.round(perDay * 30) };
});
</script>

<template>
  <div>
    <div class="mb-5 flex flex-wrap items-start justify-between gap-5">
      <div>
        <h1 class="text-3xl font-semibold tracking-[-0.03em]">{{ currentGreeting }}</h1>
        <p class="mt-2 text-sm text-fg-muted">
          Your agent's control room — what it is working on, what needs you, and what it is spending.
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <Button v-if="error" @click="load">
          <RefreshCw data-icon="inline-start" />
          Retry
        </Button>
        <template v-else-if="tasks !== null">
          <Button v-if="targets.attention.count > 0" variant="attention" as-child>
            <RouterLink :to="targets.attention.href">
              <ListChecks data-icon="inline-start" />
              {{ targets.attention.count }} need{{ targets.attention.count === 1 ? "s" : "" }} you
            </RouterLink>
          </Button>
          <Button v-if="targets.running.count > 0" variant="outline" as-child>
            <RouterLink :to="targets.running.href">
              <Route data-icon="inline-start" />
              {{ targets.running.count }} running
            </RouterLink>
          </Button>
          <Button variant="outline" as-child>
            <RouterLink to="/logs">
              <AlignLeft data-icon="inline-start" />
              Logs
            </RouterLink>
          </Button>
          <Button @click="load">
            <RefreshCw data-icon="inline-start" />
            Refresh
          </Button>
        </template>
      </div>
    </div>

    <!--
      Health. Each card takes its own content height rather than the row's:
      matching them was right while both held a checklist, but once setup is
      done that card is a title and a line while Gate pulse carries a gauge and
      four rows, so equalising left a mostly-empty box taking a quarter of the
      first screen.
    -->
    <section class="mb-5">
      <div class="mb-3 flex items-baseline gap-3 pl-1">
        <h2 class="text-xs font-semibold tracking-[0.08em] text-fg-subtle uppercase">Health</h2>
      </div>
      <div
        class="grid items-start gap-4"
        :class="omitSetup ? 'grid-cols-1' : 'grid-cols-1 min-[1080px]:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]'"
      >
        <Card v-if="setupPanel.kind === 'incomplete'">
          <CardHeader>
            <CardTitle>Finish setting up</CardTitle>
            <CardDescription>Archie needs these before it can work on its own.</CardDescription>
            <CardAction>
              <span class="text-lg font-semibold text-primary">{{ setupPct }}%</span>
            </CardAction>
          </CardHeader>
          <CardContent>
            <Progress :model-value="setupPct" class="mb-4" />
            <ul class="flex flex-col gap-3">
              <li v-for="(step, i) in setupSteps" :key="i" class="flex items-start gap-3">
                <span
                  class="mt-px grid size-[18px] flex-none place-items-center rounded-full border-[1.5px] border-border-strong text-xs text-fg-subtle"
                  :class="step.done ? 'border-transparent bg-ok-soft text-ok' : ''"
                >
                  <Check v-if="step.done" class="size-3" />
                </span>
                <div>
                  <div class="text-sm" :class="step.done ? 'text-fg-muted line-through' : ''">{{ step.title }}</div>
                  <div v-if="step.detail" class="mt-0.5 text-xs text-fg-subtle">{{ step.detail }}</div>
                </div>
              </li>
            </ul>
          </CardContent>
        </Card>

        <Card v-else-if="setupPanel.kind === 'complete'">
          <CardHeader>
            <CardTitle>Setup complete</CardTitle>
            <CardDescription>Archie is configured and ready to work.</CardDescription>
            <CardAction class="flex items-center gap-2">
              <Check class="size-4 text-ok" />
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="Dismiss setup complete"
                @click="handleDismissSetup"
              >
                <X />
              </Button>
            </CardAction>
          </CardHeader>
        </Card>

        <Card v-if="summary">
          <CardHeader>
            <CardTitle>Gate pulse</CardTitle>
            <CardDescription v-if="runTotals.runs">Work that passed its quality gates</CardDescription>
          </CardHeader>
          <CardContent>
            <Empty v-if="!runTotals.runs">
              <EmptyHeader>
                <EmptyTitle>No completed runs yet</EmptyTitle>
                <EmptyDescription>Once Archie finishes a task, its gate pass rate appears here.</EmptyDescription>
              </EmptyHeader>
            </Empty>
            <template v-else>
              <Gauge :value="(runTotals.merged / runTotals.runs) * 100" label="pass rate" />
              <ul class="mt-4 flex flex-col gap-2">
                <li
                  v-for="(w, i) in workflowStats.slice(0, 3)"
                  :key="i"
                  class="flex items-center justify-between gap-3 rounded-sm bg-muted px-3 py-2 text-sm"
                >
                  <span class="truncate text-fg-muted">{{ w.workflow || "workflow" }}</span>
                  <Badge
                    :variant="
                      (w.merged || 0) === (w.runs || 0) ? 'ok' : (w.parked || 0) > 0 ? 'warn' : 'info'
                    "
                  >
                    {{ w.merged || 0 }}/{{ w.runs || 0 }}
                  </Badge>
                </li>
                <li
                  v-if="runTotals.parked > 0"
                  class="flex items-center justify-between gap-3 rounded-sm bg-muted px-3 py-2 text-sm"
                >
                  <span class="truncate text-fg-muted">Parked, awaiting you</span>
                  <Badge variant="warn">{{ runTotals.parked }}</Badge>
                </li>
              </ul>
            </template>
          </CardContent>
        </Card>
      </div>
    </section>

    <section v-if="summary" class="mb-5">
      <div class="mb-3 flex items-baseline gap-3 pl-1">
        <h2 class="text-xs font-semibold tracking-[0.08em] text-fg-subtle uppercase">Throughput</h2>
        <span class="text-xs text-fg-subtle">Across all repositories</span>
      </div>
      <div class="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
        <StatTile
          label="Working now"
          :value="counts.running || 0"
          :compare="`${counts.queued || 0} waiting to start`"
          :series="tokenSeries"
        />
        <StatTile
          label="Needs you"
          :value="(counts.parked || 0) + (counts.waiting_human || 0)"
          :compare="counts.parked || counts.waiting_human ? 'Parked or awaiting a reply' : 'Nothing is blocked'"
          good-direction="down"
        />
        <StatTile
          label="Delivered"
          :value="(counts.merged || 0) + (counts.pr_open || 0)"
          :compare="
            Object.values(counts).reduce((a, b) => a + b, 0)
              ? `${Math.round((((counts.merged || 0) + (counts.pr_open || 0)) / Object.values(counts).reduce((a, b) => a + b, 0)) * 100)}% of all tasks`
              : 'No tasks yet'
          "
        />
        <StatTile
          label="Tokens used"
          :value="compact(tokenTotals.used)"
          :compare="`Across ${tokensByDay.length || 0} days`"
          :trend="trendPct(tokenSeries)"
          good-direction="down"
          :series="tokenSeries"
        />
      </div>
    </section>

    <section v-if="summary" class="mb-5">
      <div class="mb-3 flex items-baseline gap-3 pl-1">
        <h2 class="text-xs font-semibold tracking-[0.08em] text-fg-subtle uppercase">Right now</h2>
      </div>
      <div class="grid grid-cols-1 gap-4 min-[1080px]:grid-cols-[minmax(0,1fr)_minmax(0,1.5fr)]">
        <Card>
          <CardHeader>
            <CardTitle>Token outlook</CardTitle>
            <CardDescription v-if="tokensByDay.length">Next 30 days at the current rate</CardDescription>
          </CardHeader>
          <CardContent>
            <Empty v-if="!tokensByDay.length">
              <EmptyHeader>
                <EmptyTitle>Nothing spent yet</EmptyTitle>
                <EmptyDescription>Usage appears here once Archie runs its first task.</EmptyDescription>
              </EmptyHeader>
            </Empty>
            <template v-else>
              <div class="mb-1 text-3xl font-semibold tracking-[-0.03em]">{{ compact(tokenTotals.projected) }}</div>
              <div class="mb-4 text-xs text-fg-subtle">
                Based on {{ compact(Math.round(tokenTotals.perDay)) }} per day over {{ tokensByDay.length }} days
              </div>
              <SegmentBar
                :segments="[
                  { label: `Last 7 days (${compact(tokenTotals.recent)})`, value: tokenTotals.recent, kind: 'info' },
                  {
                    label: `Earlier (${compact(tokenTotals.used - tokenTotals.recent)})`,
                    value: Math.max(tokenTotals.used - tokenTotals.recent, 0),
                    kind: 'idle',
                  },
                ]"
              />
            </template>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Live activity</CardTitle>
            <CardDescription>Last 50, newest first</CardDescription>
            <CardAction>
              <Badge :variant="streamStateKind">{{ streamStateText }}</Badge>
            </CardAction>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Event</TableHead>
                  <TableHead>Task</TableHead>
                  <TableHead>Detail</TableHead>
                  <TableHead>When</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-if="!activity.length">
                  <TableCell colspan="4">
                    <Empty>
                      <EmptyHeader>
                        <EmptyTitle>Waiting for activity</EmptyTitle>
                        <EmptyDescription>Events appear here as Archie works.</EmptyDescription>
                      </EmptyHeader>
                    </Empty>
                  </TableCell>
                </TableRow>
                <TableRow
                  v-for="(event, i) in activity"
                  v-else
                  :key="i"
                  :class="taskIDFor(event) > 0 ? 'cursor-pointer' : ''"
                  :role="taskIDFor(event) > 0 ? 'link' : undefined"
                  :tabindex="taskIDFor(event) > 0 ? 0 : undefined"
                  :title="taskIDFor(event) > 0 ? 'Open task details' : undefined"
                  @click="taskIDFor(event) > 0 && openTask(taskIDFor(event))"
                  @keydown.enter.prevent="
                    taskIDFor(event) > 0 && openTask(taskIDFor(event))
                  "
                  @keydown.space.prevent="
                    taskIDFor(event) > 0 && openTask(taskIDFor(event))
                  "
                >
                  <TableCell class="font-medium">{{ event.kind || event.type || "event" }}</TableCell>
                  <TableCell class="font-mono">
                    {{ taskIDFor(event) > 0 ? `#${taskIDFor(event)}` : "—" }}
                  </TableCell>
                  <!--
                    The detail is cut to one line by activityDetail and clamped
                    again here so a long unbroken token (a path, a URL, a hash)
                    cannot widen the column past the table. Unclamped, this made
                    the dashboard 7,481px tall.
                  -->
                  <TableCell class="w-[55%] max-w-0">
                    <span
                      class="block truncate"
                      :title="activityDetail(event).truncated ? activityDetail(event).full : undefined"
                    >
                      {{ activityDetail(event).text }}
                    </span>
                  </TableCell>
                  <TableCell>{{ ago(event.at || Date.now()) }}</TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      </div>
    </section>
  </div>
</template>
