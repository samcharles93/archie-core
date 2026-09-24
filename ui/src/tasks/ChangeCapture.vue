<script setup lang="ts">
import { computed } from "vue";

import { ago } from "@/lib/format";

import ChangeFileTable from "./ChangeFileTable.vue";
import { captureLinks, totalsLabel } from "./changed-files";
import type { Capture, TaskRecord } from "./task-run";

/**
 * One change capture: when and where it was taken, the forge coordinates it
 * carries, and the files it recorded.
 */
const props = defineProps<{ capture: Capture; task: TaskRecord | null }>();

const links = computed(() => captureLinks(props.capture, props.task));
const prNumber = computed(() => props.capture.pr_number);
</script>

<template>
  <section>
    <div class="flex flex-wrap items-baseline justify-between gap-3">
      <span class="text-sm font-medium">
        {{
          capture.captured_after
            ? `Captured after ${capture.captured_after}`
            : "Captured"
        }}<span v-if="capture.stage"> in stage {{ capture.stage }}</span>
      </span>
      <span class="text-xs text-fg-muted">{{
        capture.captured_at ? ago(capture.captured_at) : ""
      }}</span>
    </div>
    <div
      class="mt-2 mb-3 flex flex-wrap items-center gap-3 text-xs text-fg-muted"
    >
      <span class="font-mono">
        <a
          v-if="links.repo"
          class="text-link hover:underline"
          :href="links.repo"
          target="_blank"
          rel="noreferrer"
        >
          {{ capture.owner }}/{{ capture.repo }}
        </a>
        <template v-else
          >{{ capture.owner || "" }}/{{ capture.repo || "" }}</template
        >
      </span>
      <span v-if="capture.branch" class="font-mono">
        {{ capture.branch
        }}<span v-if="capture.base"> → {{ capture.base }}</span>
      </span>
      <span v-if="capture.head_sha" class="font-mono">{{
        String(capture.head_sha).slice(0, 8)
      }}</span>
      <template v-if="prNumber">
        <a
          v-if="links.pr"
          class="text-link hover:underline"
          :href="links.pr"
          target="_blank"
          rel="noreferrer"
        >
          PR #{{ prNumber }}
        </a>
        <span v-else class="font-mono">PR #{{ prNumber }}</span>
      </template>
      <span>{{ totalsLabel(capture.totals) }}</span>
    </div>
    <ChangeFileTable :capture="capture" />
  </section>
</template>
