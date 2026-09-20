<script setup lang="ts">
import { ref, watch } from "vue";

import { Checkbox } from "@/components/ui/checkbox";
import { TableCell } from "@/components/ui/table";
import { api } from "@/lib/api";
import { errorText, loadConfig } from "./state";
import type { RepoView } from "./types";

/**
 * One boolean override on a repository row.
 *
 * The box moves before the write and moves back if the write is refused: a
 * checkbox that waits on a round trip reads as a dropped click. It follows the
 * repository's own value afterwards, so a reload that changed it elsewhere is
 * not hidden behind a stale local copy.
 */
const props = defineProps<{ repo: RepoView; field: "allow_concurrent" | "review_enabled"; editable: boolean }>();

const checked = ref(Boolean(props.repo[props.field]));
const loading = ref(false);
const error = ref<string | null>(null);

watch(
  () => props.repo[props.field],
  (next) => {
    checked.value = Boolean(next);
  },
);

async function change(next: boolean | "indeterminate"): Promise<void> {
  const value = next === true;
  checked.value = value;
  loading.value = true;
  error.value = null;
  try {
    await api.configRepoUpdate(props.repo.owner, props.repo.name, props.field, value);
    await loadConfig();
  } catch (err) {
    checked.value = !value;
    error.value = errorText(err);
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <TableCell>
    <template v-if="editable">
      <Checkbox :model-value="checked" :disabled="loading" :aria-label="field" @update:model-value="change" />
      <span v-if="error" class="ml-2 text-xs text-danger" role="alert">{{ error }}</span>
    </template>
    <template v-else>{{ checked ? "yes" : "no" }}</template>
  </TableCell>
</template>
