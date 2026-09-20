<script setup lang="ts">
import { Badge } from "@/components/ui/badge";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import type { StatusKind } from "@/lib/status";
import { statusKind, statusLabel } from "@/lib/task-meta";
import ConfigList from "./ConfigList.vue";
import ConfigRow from "./ConfigRow.vue";
import type { LifecycleEntry } from "./types";

/**
 * The statuses a task can be in, each with the severity it carries. The
 * server's own label and kind win; a status it did not describe falls back to
 * the shared lifecycle vocabulary (lib/task-meta), which is what every other
 * surface names statuses by.
 */
const props = defineProps<{ statuses: LifecycleEntry[] }>();
</script>

<template>
  <h3 class="mt-5 mb-2 text-sm font-semibold text-fg-muted first:mt-0">Statuses</h3>
  <Empty v-if="!props.statuses.length">
    <EmptyHeader>
      <EmptyTitle>No statuses reported</EmptyTitle>
      <EmptyDescription>The server did not return any lifecycle statuses.</EmptyDescription>
    </EmptyHeader>
  </Empty>
  <ConfigList v-else>
    <ConfigRow v-for="status in props.statuses" :key="status.id" :label="status.id">
      <template #value>
        <span>
          <Badge :variant="(status.kind ?? statusKind(status.id)) as StatusKind">
            {{ status.label ?? statusLabel(status.id) }}
          </Badge>
        </span>
      </template>
    </ConfigRow>
  </ConfigList>
</template>
