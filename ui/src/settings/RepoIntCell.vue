<script setup lang="ts">
import { ref, watch } from "vue";

import { Input } from "@/components/ui/input";
import { TableCell } from "@/components/ui/table";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { errorText, loadConfig } from "./state";
import type { RepoView } from "./types";

/**
 * One integer override on a repository row. The value commits on blur or on
 * Enter, not on every keystroke: half-typed numbers are not configuration.
 */
const props = defineProps<{ repo: RepoView; field: "max_retries"; editable: boolean }>();

const draft = ref(String(props.repo[props.field] ?? 0));
const loading = ref(false);
const error = ref<string | null>(null);

watch(
  () => props.repo[props.field],
  (next) => {
    draft.value = String(next ?? 0);
  },
);

function update(value: unknown): void {
  draft.value = String(value ?? "");
}

async function commit(): Promise<void> {
  const parsed = parseInt(draft.value, 10);
  if (Number.isNaN(parsed)) {
    error.value = "Enter a whole number";
    return;
  }
  loading.value = true;
  error.value = null;
  try {
    await api.configRepoUpdate(props.repo.owner, props.repo.name, props.field, parsed);
    await loadConfig();
  } catch (err) {
    error.value = errorText(err);
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <TableCell :class="cn({ 'font-mono': !editable })">
    <template v-if="editable">
      <Input
        :model-value="draft"
        :disabled="loading"
        :aria-label="field"
        class="w-24"
        autocomplete="off"
        @update:model-value="update"
        @blur="commit"
        @keydown.enter.prevent="($event.target as HTMLElement).blur()"
      />
      <span v-if="error" class="ml-2 text-xs text-danger" role="alert">{{ error }}</span>
    </template>
    <template v-else>{{ draft }}</template>
  </TableCell>
</template>
