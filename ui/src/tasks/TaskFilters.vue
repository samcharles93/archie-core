<script setup lang="ts">
import { computed } from "vue";

import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { statusList } from "@/lib/task-meta";

/**
 * The search box and the status filter. It holds no state of its own: the
 * search text and the status both belong to the page, because the query string
 * owns the status and the table needs the search text to filter.
 */

// A select item cannot carry an empty value, so "no filter" is spelled with a
// sentinel and translated at the edges of this component.
const ALL_STATUSES = "all";

const props = defineProps<{ status: string; search: string }>();

const emit = defineEmits<{
  "update:status": [status: string];
  "update:search": [search: string];
}>();

const selected = computed(() => props.status || ALL_STATUSES);

function selectStatus(value: unknown) {
  emit(
    "update:status",
    typeof value === "string" && value !== ALL_STATUSES ? value : "",
  );
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-2">
    <Input
      type="search"
      :model-value="props.search"
      aria-label="Search tasks by title or repository"
      placeholder="Search by title or repo…"
      class="w-55 max-[480px]:w-full"
      @update:model-value="emit('update:search', String($event))"
    />
    <Select :model-value="selected" @update:model-value="selectStatus">
      <SelectTrigger
        aria-label="Filter by status"
        class="w-45 max-[480px]:w-full"
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          <SelectItem :value="ALL_STATUSES">All statuses</SelectItem>
          <SelectItem value="needs_you">Needs you</SelectItem>
          <SelectItem
            v-for="entry in statusList()"
            :key="entry.id"
            :value="entry.id"
          >
            {{ entry.label }}
          </SelectItem>
        </SelectGroup>
      </SelectContent>
    </Select>
  </div>
</template>
