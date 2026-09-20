<script setup lang="ts">
import { Play, RefreshCw } from "@lucide/vue";
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
 * can only start from a parked task, so anywhere else the page says why rather
 * than offering a button that will be refused. The refusal that does arrive
 * (the task moved between the read and the click) is rendered from the error
 * class, because "you cannot do that", "your session has expired" and "the
 * daemon did not answer" need three different responses from the operator.
 */
const props = defineProps<{ id: string }>();

const run = useTaskRun();

// The action's own kind decides how loud the button is: `retry` is primary in
// the served catalogue, and would be quiet on the day it is not.
const variant = computed(() => (run.retryKind === "primary" ? "default" : "outline"));

const confirmText = computed(
  () =>
    `Start a new run for "${run.task?.title || `task ${props.id}`}"? This is a new attempt, and the previous attempt's commits are discarded.`,
);

const unavailable = computed(() => {
  if (run.canRetry || run.taskList === undefined) return "";
  return run.task
    ? `A new run can only start from a parked task. This task is ${statusLabel(run.task.status ?? "")}.`
    : "This task's operator controls could not be read, so a new run cannot be started from here.";
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
  <section class="mb-4 flex flex-wrap items-center gap-3" aria-label="Start a new run">
    <AlertDialog>
      <AlertDialogTrigger as-child>
        <Button :variant="variant" :disabled="!run.canRetry || run.retryBusy" title="Retry this task as a new run">
          <Spinner v-if="run.retryBusy" data-icon="inline-start" />
          <Play v-else data-icon="inline-start" />
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

    <p class="min-w-0 flex-1 basis-[22rem] text-sm text-fg-muted">
      Starts a new attempt for this task: a fresh worktree reset onto the base branch. The previous attempt's
      commits are discarded — they are not carried into the new run.
    </p>
    <p v-if="unavailable" class="min-w-0 flex-1 basis-[22rem] text-sm text-fg-muted">{{ unavailable }}</p>
    <Alert v-if="errorText" variant="destructive" class="basis-full">
      <AlertDescription>{{ errorText }}</AlertDescription>
    </Alert>
  </section>
</template>
