<script setup lang="ts">
import { RefreshCw } from "@lucide/vue";
import { computed } from "vue";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { statusLabel } from "@/lib/task-meta";

import { useTaskRun } from "./use-task-run";

/**
 * Restarting a task as a new run.
 *
 * The control is only live where the task itself offers the action: a new run
 * can only start from a parked task, so anywhere else the button is disabled
 * and the title says why. The refusal that does arrive (the task moved between
 * the read and the click) is rendered from the error class, because "you
 * cannot do that", "your session has expired" and "the daemon did not answer"
 * need three different responses from the operator.
 *
 * `compact` renders the bare control for the header's action row; on its own
 * the component also renders the disabled reason.
 */
const props = defineProps<{ id?: string; compact?: boolean }>();

const run = useTaskRun();

const confirmText = computed(
  () => `Start a new run for "${run.task?.title || `task ${props.id}`}"? The previous attempt's commits are discarded.`,
);

const unavailable = computed(() => {
  if (run.canRetry || run.taskList === undefined) return "";
  return run.task ? `New runs start from a parked task. This task is ${statusLabel(run.task.status ?? "")}.` : "";
});

const errorText = computed(() => {
  const err = run.retryError;
  if (!err) return "";
  if (err.kind === "session-expired") return "Your session has expired. Reload to sign in.";
  if (err.kind === "refused") return `${err.message} — this run cannot be restarted from here.`;
  return `${err.message} — try again, or check the daemon.`;
});
</script>

<template>
  <AlertDialog>
    <AlertDialogTrigger as-child>
      <Button
        :variant="run.retryKind === 'primary' ? 'default' : 'outline'"
        :disabled="!run.canRetry || run.retryBusy"
        :title="unavailable || 'Retry this task as a new run'"
      >
        <Spinner v-if="run.retryBusy" data-icon="inline-start" />
        {{ run.retryBusy ? "Starting…" : "Start a new run" }}
      </Button>
    </AlertDialogTrigger>
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>Start a new run</AlertDialogTitle>
        <AlertDialogDescription>{{ confirmText }}</AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        <AlertDialogAction @click="run.performRetry">
          <RefreshCw data-icon="inline-start" />
          Start a new run
        </AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
  <Alert v-if="errorText" variant="destructive" class="mt-2">
    <AlertDescription>{{ errorText }}</AlertDescription>
  </Alert>
</template>