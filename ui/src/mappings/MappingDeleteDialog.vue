<script setup lang="ts">
import { Trash2 } from "@lucide/vue";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { cancelDelete, confirmDelete, pendingDelete } from "./state";

/**
 * The destructive confirmation for one mapping. Deleting unprompted is not
 * offered: a playbook binding (t2db.4) points at a mapping by id, so removing
 * one without being asked would break a binding the operator still has.
 */
function onOpenChange(open: boolean) {
  if (!open) cancelDelete();
}
</script>

<template>
  <AlertDialog :open="pendingDelete !== null" @update:open="onOpenChange">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogMedia class="bg-danger-soft text-danger">
          <Trash2 />
        </AlertDialogMedia>
        <AlertDialogTitle>Delete this mapping?</AlertDialogTitle>
        <AlertDialogDescription>
          "{{ pendingDelete?.name }}" will be removed. Anything bound to it
          stops resolving.
        </AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel @click="cancelDelete">Cancel</AlertDialogCancel>
        <AlertDialogAction variant="destructive" @click="confirmDelete"
          >Delete</AlertDialogAction
        >
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
