<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";

import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { LOG_LEVELS } from "@/lib/log";
import { componentOptions, filters } from "./state";

/**
 * The list's filters: level, component and a search over messages and fields.
 *
 * The level and component pair are two-way on the filter state, because a
 * change to either is a new read; the search box commits after a pause,
 * because a keystroke is not a request.
 */

// reka-ui refuses an empty string as a SelectItem value -- it is how the
// component spells "nothing selected". The wire wants an absent filter for
// "all", so the sentinel converts at this boundary and everything downstream
// (the filter state, the query string, the server) keeps the empty value.
const ALL = "__all__";

// LOG_LEVELS' values are a wire contract: the server matches that CSV
// server-side, so the labels are paired with them here and never rewritten.
const levelOptions = LOG_LEVELS.map((option) => ({
  value: option.value || ALL,
  label: option.label,
}));
const componentSelectOptions = computed(() => [
  { value: ALL, label: "All components" },
  ...componentOptions.value.map((name) => ({ value: name, label: name })),
]);

const level = computed(() => filters.level || ALL);
const component = computed(() => filters.component || ALL);

function setLevel(value: unknown): void {
  filters.level = value === ALL ? "" : String(value ?? "");
}

function setComponent(value: unknown): void {
  filters.component = value === ALL ? "" : String(value ?? "");
}

// Seeded from the filter rather than from nothing: the filters outlive a visit
// to this page, and a box that came back empty while the list was still
// narrowed by its query would be claiming the list is unfiltered.
const search = ref(filters.q);
let searchTimer: ReturnType<typeof setTimeout> | undefined;

watch(search, (value) => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    filters.q = value;
  }, 250);
});

onUnmounted(() => clearTimeout(searchTimer));
</script>

<template>
  <div class="flex min-w-0 flex-wrap items-center gap-2">
    <Select :model-value="level" @update:model-value="setLevel">
      <SelectTrigger size="sm" class="w-44" aria-label="Level">
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

    <Select :model-value="component" @update:model-value="setComponent">
      <SelectTrigger size="sm" class="w-44" aria-label="Component">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          <SelectItem
            v-for="option in componentSelectOptions"
            :key="option.value"
            :value="option.value"
          >
            {{ option.label }}
          </SelectItem>
        </SelectGroup>
      </SelectContent>
    </Select>

    <Input
      v-model="search"
      type="search"
      placeholder="Search messages and fields…"
      class="min-w-0 flex-1 md:max-w-56"
    />
  </div>
</template>
