<script setup lang="ts">
import { computed } from "vue";

import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { LOG_LEVELS } from "@/lib/log";

import type { LogFilters } from "./task-run";

/**
 * The log pane's filter row, and the attempt it is showing.
 *
 * The attempt is not a filter: the page selects the attempt and this row
 * reports which one it is, so the pane can never read attempt 1's log while
 * claiming attempt 2.
 *
 * The two "everything" options need a sentinel value: an empty string is how a
 * Select spells "nothing selected", so `""` cannot also be an item. The CSV
 * LOG_LEVELS carries stays exactly as it is -- the wire contract is the values,
 * not the widgets.
 */
const ALL = "__all__";

const props = defineProps<{
  filters: LogFilters;
  stages: string[];
  attempt: number;
}>();

const emit = defineEmits<{ filter: [next: LogFilters] }>();

const levelOptions = computed(() => LOG_LEVELS.map((level) => ({ value: level.value || ALL, label: level.label })));
const levelValue = computed(() => props.filters.level || ALL);
const stageValue = computed(() => props.filters.stage || ALL);

function change(patch: Partial<LogFilters>): void {
  emit("filter", { ...props.filters, ...patch });
}

function setLevel(value: unknown): void {
  change({ level: String(value) === ALL ? "" : String(value) });
}

function setStage(value: unknown): void {
  change({ stage: String(value) === ALL ? "" : String(value) });
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-3 border-b border-border pb-3">
    <span class="text-sm font-medium">{{ attempt > 0 ? `Attempt ${attempt}` : "Current attempt" }}</span>

    <div class="inline-flex items-center gap-2">
      <Select :model-value="levelValue" aria-label="Filter log entries by level" @update:model-value="setLevel">
        <SelectTrigger size="sm">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem v-for="option in levelOptions" :key="option.value" :value="option.value">
              {{ option.label }}
            </SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
    </div>

    <div class="inline-flex items-center gap-2">
      <Select :model-value="stageValue" aria-label="Filter log entries by stage" @update:model-value="setStage">
        <SelectTrigger size="sm">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem :value="ALL">All stages</SelectItem>
            <SelectItem v-for="stage in stages" :key="stage" :value="stage">{{ stage }}</SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
    </div>

    <!-- A stage filter is a narrowing, not a clean sweep: only lines a stage
         tagged carry a stage, and agent/tool output never does. Saying so
         beside the control is what keeps a sparse result from reading as a
         broken pane. -->
    <p v-if="filters.stage" class="basis-full text-xs text-fg-muted">
      Only lines a stage tagged are shown; agent and tool output carries none.
    </p>
  </div>
</template>
