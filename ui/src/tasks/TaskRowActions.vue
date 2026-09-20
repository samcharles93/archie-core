<script setup lang="ts">
import { computed, ref } from "vue";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { api, classifyActionError, type ActionErrorKind } from "@/lib/api";
import { actionFor, type ActionMeta } from "@/lib/task-meta";
import type { Task } from "./TaskRow.vue";

/**
 * The lifecycle controls for one task. A task offers whatever the server says
 * it offers, so the decisions here are presentation only: which variant a
 * control wears, whether it needs confirming, and how a refusal reads.
 */

/** A control with its forge target already resolved. */
interface Control {
  id: string;
  label: string;
  kind: string;
  confirm?: string;
  href: string;
}

type ControlVariant = "default" | "ghost" | "destructive";

const props = defineProps<{ task: Task }>();
const emit = defineEmits<{ done: [taskId: Task["id"]] }>();

const inFlight = ref(false);
const error = ref<{ kind: ActionErrorKind; message: string } | null>(null);

// The dialog's open flag and the action it will run are two refs, not one:
// pressing the dialog's action closes it, and that close clears the open flag
// before the action's own handler reads it.
const confirming = ref<string | null>(null);
const confirmingId = ref<string | null>(null);

const controls = computed<Control[]>(() =>
  (props.task.actions ?? [])
    .map((id) => actionFor(id))
    .filter((meta): meta is ActionMeta => meta !== null)
    .map((meta) => ({ ...meta, href: meta.kind === "link" ? forgeLink(meta.id) : "" })),
);

const confirmingMeta = computed(() => (confirmingId.value ? actionFor(confirmingId.value) : null));
const confirmText = computed(() =>
  confirmingMeta.value?.confirm
    ? confirmingMeta.value.confirm.replaceAll("{title}", props.task.title || "this task")
    : "",
);
const confirmLabel = computed(() => confirmingMeta.value?.label || "Confirm");

// A refused action ("you can't do that" -- bad input, a conflicting task state)
// is the operator's to correct; a broken one (5xx, network drop, timeout) is
// the daemon's, and says so. An expired session is neither, and gets its own
// treatment above rather than either framing.
const errorText = computed(() => {
  if (!error.value) return "";
  if (error.value.kind === "refused") return `${error.value.message} — you can't do that.`;
  return `${error.value.message} — try again, or check the daemon.`;
});

// A link control points at the forge artefact the action names. Chat-sourced
// work has no issue on a forge, and a task that never opened a pull request has
// no PR to point at, so the control is disabled rather than a dead link.
function forgeLink(id: string): string {
  if (id === "open_pr") {
    return props.task.pr_url && props.task.pr_number ? props.task.pr_url : "";
  }
  if (id === "open_issue") {
    if (props.task.source === "chat" || !props.task.issue_number) return "";
    return props.task.issue_url ?? "";
  }
  return "";
}

function variantFor(kind: string): ControlVariant {
  if (kind === "quiet") return "ghost";
  if (kind === "danger") return "destructive";
  return "default";
}

// A control that names a consequence is confirmed first; the two that only open
// a forge page are not, because they change nothing.
function request(id: string) {
  error.value = null;
  if (actionFor(id)?.confirm) {
    confirmingId.value = id;
    confirming.value = id;
    return;
  }
  void run(id);
}

async function run(id: string) {
  confirming.value = null;
  inFlight.value = true;
  error.value = null;
  try {
    // The route path and the API both address a task by string; the list serves
    // ids as numbers, so the row's own id is narrowed once, here.
    await api.taskAction(String(props.task.id), id);
    emit("done", props.task.id);
  } catch (err) {
    error.value = classifyActionError(err);
  } finally {
    inFlight.value = false;
  }
}

function onConfirmationOpen(open: boolean) {
  if (!open) confirming.value = null;
}

// A full reload, not a router navigation: the point is to re-establish the
// session with whatever is in front of archied, which a client-side route
// change would leave exactly as it was.
function reloadPage() {
  window.location.reload();
}
</script>

<template>
  <div class="flex flex-col items-end gap-1">
    <Alert
      v-if="error"
      variant="destructive"
      class="w-56 gap-0.5 px-2 py-1.5 text-xs whitespace-normal"
    >
      <template v-if="error.kind === 'session-expired'">
        <AlertTitle class="text-xs">Your session has expired</AlertTitle>
        <AlertDescription class="text-xs">
          <Button size="xs" variant="outline" @click.stop="reloadPage">Reload to sign in</Button>
        </AlertDescription>
      </template>
      <AlertDescription v-else class="text-xs">{{ errorText }}</AlertDescription>
    </Alert>

    <!--
      One line, never wrapped: three controls on two lines doubled the height of
      every actionable row. Keystrokes stop at this container rather than
      bubbling to the row, which would open the task as well as press the button.
    -->
    <div class="flex items-center gap-1" @keydown.stop>
      <template v-for="control in controls" :key="control.id">
        <template v-if="control.kind === 'link'">
          <Button v-if="control.href" as-child variant="outline" size="sm">
            <a :href="control.href" target="_blank" rel="noreferrer" @click.stop>{{ control.label }}</a>
          </Button>
          <Button v-else variant="outline" size="sm" disabled :title="`${control.label} is unavailable`">
            {{ control.label }}
          </Button>
        </template>
        <Button
          v-else
          :variant="variantFor(control.kind)"
          size="sm"
          :disabled="inFlight"
          @click.stop="request(control.id)"
        >
          {{ control.label }}
        </Button>
      </template>
    </div>

    <AlertDialog :open="confirming !== null" @update:open="onConfirmationOpen">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{{ confirmLabel }} this task?</AlertDialogTitle>
          <AlertDialogDescription>{{ confirmText }}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Keep it</AlertDialogCancel>
          <AlertDialogAction
            :variant="variantFor(confirmingMeta?.kind ?? '')"
            @click="confirmingId && run(confirmingId)"
          >
            {{ confirmLabel }}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
