<script setup lang="ts">
import { computed } from "vue";

import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { LOG_LEVELS } from "@/lib/log";

import type { LogFilters } from "./task-run";

/**
 * The log pane's filter row.
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
}>();

const emit = defineEmits<{ filter: [next: LogFilters] }>();

const levelOptions = computed(() =>
  LOG_LEVELS.map((level) => ({
    value: level.value || ALL,
    label: level.label,
  })),
);
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
    <div class="inline-flex items-center gap-2">
      <Select
        :model-value="levelValue"
        aria-label="Filter log entries by level"
        @update:model-value="setLevel"
      >
        <SelectTrigger size="sm">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem
              v-for="option in levelOptions"
              :key="option.value"
              :value="option.value"
            >
              {{ option.label }}
            </SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
    </div>

    <div class="inline-flex items-center gap-2">
      <Select
        :model-value="stageValue"
        aria-label="Filter log entries by stage"
        @update:model-value="setStage"
      >
        <SelectTrigger size="sm">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem :value="ALL">All stages</SelectItem>
            <SelectItem v-for="stage in stages" :key="stage" :value="stage">{{
              stage
            }}</SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
    </div>

  </div>
</template>
