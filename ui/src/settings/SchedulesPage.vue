<script setup lang="ts">
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";
import { CalendarClock, Plus, Trash2 } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { DurationInput } from "@/components/ui/duration-input";
import { Input } from "@/components/ui/input";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { SettingRow } from "@/components/ui/setting-row";
import { Textarea } from "@/components/ui/textarea";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import HistoryLink from "./HistoryLink.vue";

const KIND = "schedules";

/** A scheduled job; only the "workflow" kind is wired, so every job is one. */
interface Job {
  id: string;
  kind: string;
  detail?: string;
  pool?: string;
  schedule: { kind?: string; interval?: string; cron?: string; at?: string };
  target: Record<string, unknown>;
  payload: { text?: string };
  next_run?: string;
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "schedules"));
const jobs = computed(() => store.drafts[KIND]?.value as Job[] | undefined);
const error = computed(() => store.stateFor(KIND).error);

const whenKinds = [
  { value: "interval", label: "Every" },
  { value: "cron", label: "Cron" },
  { value: "once", label: "Once" },
];
const pools = [
  { value: "", label: "Sequential" },
  { value: "parallel", label: "Parallel" },
];

function add() {
  jobs.value?.push({
    id: "",
    kind: "workflow",
    detail: "",
    pool: "",
    schedule: { kind: "interval", interval: "24h0m0s" },
    target: {},
    payload: { text: "" },
  });
}

// datetime-local speaks local wall time without a zone; the document stores
// an RFC 3339 instant.
function toLocalInput(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
function fromLocalInput(value: string): string | undefined {
  return value ? new Date(value).toISOString() : undefined;
}
const whenKind = (job: Job) => job.schedule.kind || "interval";
</script>

<template>
  <div>
    <PageHeader title="Schedules">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
      <Button v-if="jobs?.length" size="sm" @click="add"><Plus data-icon="inline-start" /> Add schedule</Button>
    </PageHeader>
    <p class="-mt-4 mb-8 text-sm text-fg-muted">Work Archie starts on its own, on a timetable.</p>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">
      {{ catalogError || error }}
    </p>

    <template v-if="jobs">
      <div v-if="!jobs.length" class="flex flex-col items-center rounded-lg border border-border bg-card px-6 py-12 text-center">
        <span class="mb-3 grid size-10 place-items-center rounded-md bg-secondary text-muted-foreground">
          <CalendarClock class="size-5" aria-hidden="true" />
        </span>
        <p class="font-medium">No schedules yet</p>
        <p class="mt-1 mb-4 text-sm text-fg-muted">A schedule starts a task on an interval, a cron line, or once.</p>
        <Button size="sm" @click="add"><Plus data-icon="inline-start" /> Add schedule</Button>
      </div>

      <section
        v-for="(job, i) in jobs"
        :key="i"
        class="mb-4 rounded-lg border border-border bg-card px-5 pt-4 pb-2"
        :aria-label="job.detail || job.id || 'New schedule'"
      >
        <header class="flex items-center gap-2">
          <h2 class="min-w-0 flex-1 truncate text-[15px] font-medium">{{ job.detail || job.id || "New schedule" }}</h2>
          <span v-if="job.next_run" class="text-xs text-fg-subtle">Next run {{ new Date(job.next_run).toLocaleString() }}</span>
          <Button variant="ghost" size="icon" aria-label="Delete schedule" @click="jobs.splice(i, 1)"><Trash2 /></Button>
        </header>
        <SettingRow label="ID" :for="`job-${i}-id`" hint="Unique. Runs are recorded against it.">
          <Input :id="`job-${i}-id`" v-model="job.id" class="max-w-sm font-mono" :aria-invalid="!job.id.trim() || undefined" />
        </SettingRow>
        <SettingRow label="Task title" :for="`job-${i}-title`" hint="The title of each task it starts. Empty uses the ID.">
          <Input :id="`job-${i}-title`" v-model="job.detail" class="max-w-md" />
        </SettingRow>
        <SettingRow label="Instructions" :for="`job-${i}-text`" hint="What the task is asked to do.">
          <Textarea :id="`job-${i}-text`" v-model="job.payload.text" :rows="3" />
        </SettingRow>
        <SettingRow label="When" hint="An interval, a five-field cron line, or a single time.">
          <div class="flex flex-wrap items-center gap-3">
            <SegmentedControl
              :model-value="whenKind(job)"
              label="When"
              :options="whenKinds"
              @update:model-value="(k: string) => (job.schedule = { kind: k, interval: k === 'interval' ? '24h0m0s' : undefined })"
            />
            <DurationInput v-if="whenKind(job) === 'interval'" v-model="job.schedule.interval!" :units="['m', 'h']" />
            <Input
              v-else-if="whenKind(job) === 'cron'"
              v-model="job.schedule.cron"
              class="w-48 font-mono"
              placeholder="0 9 * * 1-5"
              aria-label="Cron expression"
            />
            <Input
              v-else
              type="datetime-local"
              class="w-60 font-mono"
              aria-label="Run at"
              :model-value="toLocalInput(job.schedule.at)"
              @update:model-value="(v) => (job.schedule.at = fromLocalInput(String(v)))"
            />
          </div>
        </SettingRow>
        <SettingRow label="Overlap" hint="Sequential waits for the previous run to finish.">
          <SegmentedControl :model-value="job.pool ?? ''" label="Overlap" :options="pools" @update:model-value="(p: string) => (job.pool = p)" />
        </SettingRow>
      </section>
    </template>
  </div>
</template>
