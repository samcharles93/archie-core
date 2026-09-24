<script setup lang="ts">
import { Ellipsis } from "@lucide/vue";
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
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
    .map((meta) => ({
      ...meta,
      href: meta.kind === "link" ? forgeLink(meta.id) : "",
    })),
);

//
// One primary plus overflow: a server-declared action list can hold seven
// controls and two forge links, and a row that shows all of them as buttons in
// one line is unreadable. So the first non-link control keeps a visible button
// of its own; every other non-link control folds behind a '···' menu that runs
// the identical request path, and the forge links stay inline after the
// primary because they navigate rather than act.
const nonLinkControls = computed<Control[]>(() =>
  controls.value.filter((c) => c.kind !== "link"),
);
const primaryControl = computed<Control | null>(
  () => nonLinkControls.value[0] ?? null,
);
const overflowControls = computed<Control[]>(() =>
  nonLinkControls.value.slice(1),
);
const linkControls = computed<Control[]>(() =>
  controls.value.filter((c) => c.kind === "link"),
);

const menuOpen = ref(false);

const confirmingMeta = computed(() =>
  confirmingId.value ? actionFor(confirmingId.value) : null,
);
const confirmText = computed(() =>
  confirmingMeta.value?.confirm
    ? confirmingMeta.value.confirm.replaceAll(
        "{title}",
        props.task.title || "this task",
      )
    : "",
);
const confirmLabel = computed(() => confirmingMeta.value?.label || "Confirm");

// A refused action ("you can't do that" -- bad input, a conflicting task state)
// is the operator's to correct; a broken one (5xx, network drop, timeout) is
// the daemon's, and says so. Authentication failures are surfaced by the app
// shell because they affect every request, not only this row.
const errorText = computed(() => {
  if (!error.value) return "";
  if (error.value.kind === "session-expired")
    return "Dashboard authentication is required.";
  if (error.value.kind === "refused")
    return `${error.value.message} — you can't do that.`;
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

// A menu item must close its menu before the dialog can take focus, so both
// surfaces funnel through the same request() once the menu is down; the
// inFlight disabling and the error handling stay identical either way.
function requestFromMenu(id: string) {
  menuOpen.value = false;
  request(id);
}
</script>

<template>
  <div class="flex flex-col items-end gap-1">
    <Alert
      v-if="error"
      variant="destructive"
      class="w-56 gap-0.5 px-2 py-1.5 text-xs whitespace-normal"
    >
      <AlertTitle v-if="error.kind === 'session-expired'" class="text-xs"
        >Authentication required</AlertTitle
      >
      <AlertDescription class="text-xs">{{ errorText }}</AlertDescription>
    </Alert>

    <!--
      One line, never wrapped: three controls on two lines doubled the height of
      every actionable row. The one-primary-plus-overflow rule (see the script)
      keeps the line short; keystrokes stop at this container rather than
      bubbling to the row, which would open the task as well as press the
      button.
    -->
    <div class="flex items-center gap-1" @keydown.stop>
      <Button
        v-if="primaryControl"
        :variant="variantFor(primaryControl.kind)"
        size="sm"
        :disabled="inFlight"
        @click.stop="request(primaryControl.id)"
      >
        {{ primaryControl.label }}
      </Button>

      <template v-for="control in linkControls" :key="control.id">
        <Button v-if="control.href" as-child variant="ghost" size="sm">
          <a
            :href="control.href"
            target="_blank"
            rel="noreferrer"
            @click.stop
            >{{ control.label }}</a
          >
        </Button>
        <Button
          v-else
          variant="ghost"
          size="sm"
          disabled
          :title="`${control.label} is unavailable`"
        >
          {{ control.label }}
        </Button>
      </template>

      <DropdownMenu v-if="overflowControls.length" v-model:open="menuOpen">
        <DropdownMenuTrigger as-child>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="More actions"
            :disabled="inFlight"
            @click.stop
          >
            <Ellipsis :size="14" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem
            v-for="control in overflowControls"
            :key="control.id"
            :variant="control.kind === 'danger' ? 'destructive' : 'default'"
            :disabled="inFlight"
            @click.stop="requestFromMenu(control.id)"
          >
            {{ control.label }}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
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
