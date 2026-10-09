<script setup lang="ts">
import ApplyStatusRows from "./ApplyStatusRows.vue";
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";
import { CalendarClock, Plus, Trash2 } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyContent,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { DurationInput } from "@/components/ui/duration-input";
import { Input } from "@/components/ui/input";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { SettingRow } from "@/components/ui/setting-row";
import { Textarea } from "@/components/ui/textarea";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import DraftHint from "./DraftHint.vue";
import HistoryLink from "./HistoryLink.vue";

const KIND = "schedules";

/** A scheduled job: "workflow" starts a task, "chat" sends a message. */
interface Job {
  id: string;
  kind: string;
  detail?: string;
  pool?: string;
  schedule: { kind?: string; interval?: string; cron?: string; at?: string };
  target: { channel?: string; chat_id?: string };
  payload: { text?: string };
  next_run?: string;
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "schedules"));
const jobs = computed(() => store.drafts[KIND]?.value as Job[] | undefined);
const error = computed(() => store.pageErrorFor(KIND));
const issues = computed(() => store.issuesFor(KIND));
const jobIssue = (i: number) => issues.value.find((entry) => entry.path === `${i}.id`);

const whenKinds = [
  { value: "interval", label: "Every" },
  { value: "cron", label: "Cron" },
  { value: "once", label: "Once" },
];
const jobKinds = [
  { value: "workflow", label: "Run a task" },
  { value: "chat", label: "Send a message" },
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
      <Button v-if="jobs?.length" size="sm" @click="add"><Plus data-icon="inline-start" />Add schedule</Button>
    </PageHeader>
    <ApplyStatusRows v-for="resource in resources" :key="resource.kind" :kind="resource.kind" class="mb-4" />

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">
      {{ catalogError || error }}
    </p>

    <template v-if="jobs">
      <Empty v-if="!jobs?.length">
        <EmptyMedia variant="icon">
          <CalendarClock />
        </EmptyMedia>
        <EmptyHeader>
          <EmptyTitle>No schedules yet</EmptyTitle>
        </EmptyHeader>
        <EmptyContent>
          <Button size="sm" @click="add"><Plus data-icon="inline-start" />Add schedule</Button>
        </EmptyContent>
      </Empty>

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
        <SettingRow label="ID" :for="`job-${i}-id`">
          <div class="flex flex-wrap items-center gap-3">
            <Input :id="`job-${i}-id`" v-model="job.id" class="max-w-sm font-mono" :aria-invalid="jobIssue(i) ? true : undefined" />
            <DraftHint :kind="KIND" :path="`${i}.id`" />
          </div>
          <p v-if="jobIssue(i)" class="mt-1.5 text-xs text-danger">{{ jobIssue(i)?.message }}</p>
        </SettingRow>
        <SettingRow label="Does">
          <div class="flex flex-wrap items-center gap-3">
            <SegmentedControl
              :model-value="job.kind"
              label="Does"
              :options="jobKinds"
              @update:model-value="(k: string) => { job.kind = k; job.target = k === 'chat' ? { channel: 'telegram', chat_id: '' } : {}; }"
            />
            <DraftHint :kind="KIND" :path="`${i}.kind`" />
          </div>
        </SettingRow>
        <SettingRow v-if="job.kind === 'chat'" label="To" :for="`job-${i}-chat`">
          <div class="flex flex-wrap items-center gap-3">
            <Input v-model="job.target.channel" class="w-32 font-mono" aria-label="Channel" placeholder="telegram" />
            <Input :id="`job-${i}-chat`" v-model="job.target.chat_id" class="w-48 font-mono" aria-label="Chat ID" placeholder="Chat ID" />
            <DraftHint :kind="KIND" :path="`${i}.target.chat_id`" />
          </div>
        </SettingRow>
        <SettingRow v-else label="Task title" :for="`job-${i}-title`" hint="Empty: the ID.">
          <div class="flex flex-wrap items-center gap-3">
            <Input :id="`job-${i}-title`" v-model="job.detail" class="max-w-md" />
            <DraftHint :kind="KIND" :path="`${i}.detail`" />
          </div>
        </SettingRow>
        <SettingRow :label="job.kind === 'chat' ? 'Message' : 'Instructions'" :for="`job-${i}-text`">
          <Textarea :id="`job-${i}-text`" v-model="job.payload.text" :rows="3" />
          <DraftHint :kind="KIND" :path="`${i}.payload.text`" />
        </SettingRow>
        <SettingRow label="When">
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
            <DraftHint
              :kind="KIND"
              :path="whenKind(job) === 'cron' ? `${i}.schedule.cron` : whenKind(job) === 'once' ? `${i}.schedule.at` : `${i}.schedule.interval`"
            />
            <DraftHint :kind="KIND" :path="`${i}.schedule.kind`" />
          </div>
        </SettingRow>
        <SettingRow label="Overlap">
          <div class="flex flex-wrap items-center gap-3">
            <SegmentedControl :model-value="job.pool ?? ''" label="Overlap" :options="pools" @update:model-value="(p: string) => (job.pool = p)" />
            <DraftHint :kind="KIND" :path="`${i}.pool`" />
          </div>
        </SettingRow>
      </section>
    </template>
  </div>
</template>
