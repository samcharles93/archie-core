<script lang="ts">
import { attentionStatusIds, statusIds } from "@/lib/task-meta";

// The vocabulary is the server catalog, not a hand-synced copy: the "needs
// you" grouping and the set of known statuses come straight from task-meta so
// a status added on the backend shows up here without a frontend change.
// "needs_you" is a UI pseudo-status (work waiting on a human), so it is
// prepended rather than stored in the catalog.
//
// Both are read at call time rather than captured at import. The catalog
// arrives after this module loads, so module-level constants here would freeze
// the defaults for the life of the process -- which is what they did, leaving
// the task filter unable to see a served status no matter when it landed.
export function taskStatuses(): Set<string> {
  return new Set(["needs_you", ...statusIds()]);
}

/**
 * A ?status= value, kept only when the catalog knows it: the query string is
 * operator input, and an unknown status would filter the whole board away with
 * no control showing why.
 */
export function initialTaskFilter(requested: string | null | undefined): string {
  return requested && taskStatuses().has(requested) ? requested : "";
}

export function taskMatchesStatus(task: { status?: string }, status: string): boolean {
  if (!status) return true;
  if (status === "needs_you") return attentionStatusIds().has(task.status ?? "");
  return task.status === status;
}
</script>

<script setup lang="ts">
import { computed } from "vue";

import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
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
  emit("update:status", typeof value === "string" && value !== ALL_STATUSES ? value : "");
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
      <SelectTrigger aria-label="Filter by status" class="w-45 max-[480px]:w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          <SelectItem :value="ALL_STATUSES">All statuses</SelectItem>
          <SelectItem value="needs_you">Needs you</SelectItem>
          <SelectItem v-for="entry in statusList()" :key="entry.id" :value="entry.id">
            {{ entry.label }}
          </SelectItem>
        </SelectGroup>
      </SelectContent>
    </Select>
  </div>
</template>
