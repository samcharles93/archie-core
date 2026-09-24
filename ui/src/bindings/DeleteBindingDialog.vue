<script setup lang="ts">
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
import type { Binding } from "./binding-draft";

/** Confirming a delete, and nothing else. A binding cannot be recovered, so
 * the action is behind a dialog rather than a click. */
const props = defineProps<{ binding: Binding | null }>();
const emit = defineEmits<{ confirm: [binding: Binding]; cancel: [] }>();

// The binding is handed back by value so the confirmation stands on its own:
// the dialog closes on the click, and the caller must not have to re-read
// state that the close has already cleared.
function confirm(): void {
  if (props.binding) emit("confirm", props.binding);
}

// Reka closes the dialog on the action click, so this is what tells the caller
// to forget the pending binding -- whether the delete happened or not.
function onOpenChange(open: boolean): void {
  if (!open) emit("cancel");
}
</script>

<template>
  <AlertDialog :open="props.binding !== null" @update:open="onOpenChange">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>Delete "{{ props.binding?.name }}"?</AlertDialogTitle>
        <AlertDialogDescription>
          It stops dispatching immediately and cannot be recovered. Captured
          events for its source keep arriving; they just stop starting tasks.
        </AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        <AlertDialogAction variant="destructive" @click="confirm"
          >Delete</AlertDialogAction
        >
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
