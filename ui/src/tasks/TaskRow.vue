<script lang="ts">
/**
 * The task fields this folder reads. One shape for the list, its table, its
 * filters and the row itself, declared on the component that renders it.
 */
export interface Task {
  id: number | string;
  title?: string;
  owner?: string;
  repo?: string;
  repo_url?: string;
  issue_number?: number;
  issue_url?: string;
  pr_number?: number;
  pr_url?: string;
  source?: string;
  status?: string;
  workflow?: string;
  stage?: string;
  attempt?: number;
  created_at?: string;
  updated_at?: string;
  actions?: string[];
}
</script>

<script setup lang="ts">
import { computed } from "vue";
import { RouterLink, useRouter } from "vue-router";

import { Badge } from "@/components/ui/badge";
import { TableCell, TableRow } from "@/components/ui/table";
import { ago } from "@/lib/format";
import type { StatusKind } from "@/lib/status";
import { statusKind, statusLabel } from "@/lib/task-meta";
import TaskRowActions from "./TaskRowActions.vue";

/**
 * One task. The whole row opens the task's run detail, which is why it carries
 * link semantics and a keyboard path of its own: every control inside it has to
 * stop the event on its way out, or activating a button would also navigate.
 */
const props = withDefaults(defineProps<{ task: Task; showRepo?: boolean }>(), { showRepo: true });

const emit = defineEmits<{ done: [taskId: Task["id"]] }>();

const router = useRouter();

// Lifecycle vocabulary is the served catalog, never a hand-synced copy: the
// label and the severity both come from task-meta, so a status the backend
// knows renders with the right words and the right colour.
const label = computed(() => statusLabel(props.task.status ?? ""));
const kind = computed(() => statusKind(props.task.status ?? "") as StatusKind);
const title = computed(() => props.task.title || "untitled task");

// Chat-sourced work has no forge issue behind it, so there is no page to open
// and the number stands on its own.
const issueHref = computed(() => {
  if (props.task.source === "chat") return "";
  if (!props.task.issue_url || !props.task.issue_number) return "";
  return props.task.issue_url;
});

function open() {
  void router.push(`/tasks/${props.task.id}`);
}
</script>

<template>
  <TableRow
    :id="`task-row-${props.task.id}`"
    tabindex="0"
    role="link"
    :aria-label="`Open the run detail for ${title}`"
    class="cursor-pointer"
    @click="open"
    @keydown.enter.prevent="open"
    @keydown.space.prevent="open"
  >
    <!--
      The repo column is dropped when every visible row shares it, so the width
      it would repeat goes to the title instead. Truncated rather than wrapped:
      an owner/name is one token and breaking it mid-word reads as two.
    -->
    <TableCell v-if="props.showRepo" class="text-fg-muted max-w-40 truncate">
      <a
        v-if="props.task.repo_url"
        class="hover:text-foreground hover:underline"
        :href="props.task.repo_url"
        target="_blank"
        rel="noreferrer"
        :title="`Open ${props.task.owner}/${props.task.repo} on the forge`"
        @click.stop
        @keydown.stop
      >
        {{ props.task.owner }}/{{ props.task.repo }}
      </a>
      <template v-else>{{ props.task.owner }}/{{ props.task.repo }}</template>
    </TableCell>
    <TableCell class="text-fg-muted">
      <a
        v-if="issueHref"
        class="hover:text-foreground hover:underline"
        :href="issueHref"
        target="_blank"
        rel="noreferrer"
        :title="`Open issue #${props.task.issue_number} on the forge`"
        @click.stop
        @keydown.stop
      >
        #{{ props.task.issue_number }}
      </a>
      <template v-else>#{{ props.task.issue_number }}</template>
    </TableCell>
    <!--
      The floor belongs on the link, not the cell: a table cell ignores
      min-width, so a floor there is dropped and the title wraps to one word a
      line as soon as the table is narrower than its columns.
    -->
    <TableCell class="w-full whitespace-normal">
      <RouterLink
        class="inline-block min-w-40 hover:underline"
        :to="`/tasks/${props.task.id}`"
        title="Open the run detail for this task"
        @click.stop
        @keydown.stop
      >
        {{ title }}
      </RouterLink>
    </TableCell>
    <TableCell>
      <Badge :variant="kind">{{ label }}</Badge>
    </TableCell>
    <TableCell class="text-fg-muted">{{ props.task.workflow || "—" }}</TableCell>
    <TableCell class="text-fg-muted">{{ props.task.stage || "—" }}</TableCell>
    <TableCell
      class="text-fg-muted"
      :title="props.task.created_at ? `Created ${ago(props.task.created_at)}` : ''"
    >
      {{ ago(props.task.updated_at) }}
    </TableCell>
    <TableCell>
      <TaskRowActions :task="props.task" @done="emit('done', $event)" />
    </TableCell>
  </TableRow>
</template>
