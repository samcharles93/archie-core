<script setup lang="ts">
import { computed, ref } from "vue";

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
import { decideDangerous } from "./state";
import type { DangerousAction } from "./types";

/**
 * One queued request: what it would do, and the decisions open on it. Approve
 * runs it once, "Approve for 24h" runs it for a day, and Deny drops it.
 *
 * Both approves are destructive-weighted and confirmed first, because they
 * grant authority: approve for the one run, the 24h option for every future
 * request of the same family. Deny takes nothing away, so it stays plain.
 */
const props = defineProps<{ action: DangerousAction }>();

// Which approve decision is awaiting its confirmation, if any: one dialog
// driven by the decision it will run, rather than two copies of the same
// footer. The decision kinds are fixed by the endpoint ('approve' |
// 'permanent' | 'deny') and must not be reworded here.
const confirming = ref<"approve" | "permanent" | null>(null);

const description = computed(() => props.action.description || "this action");

function decide(decision: "approve" | "permanent" | "deny"): void {
  void decideDangerous(props.action.id, decision);
}

function onConfirmationOpen(open: boolean): void {
  if (!open) confirming.value = null;
}
</script>

<template>
  <div class="flex flex-wrap items-center justify-between gap-3 border-t border-hairline py-3">
    <span class="text-sm">{{ props.action.description }}</span>
    <div class="flex flex-wrap gap-2">
      <Button variant="destructive" @click="confirming = 'approve'">Approve</Button>
      <Button variant="destructive" @click="confirming = 'permanent'">Approve for 24h</Button>
      <Button variant="default" @click="decide('deny')">Deny</Button>
    </div>
  </div>

  <AlertDialog :open="confirming !== null" @update:open="onConfirmationOpen">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>
          {{ confirming === 'permanent' ? 'Approve for 24 hours?' : 'Approve this action?' }}
        </AlertDialogTitle>
        <AlertDialogDescription>
          <template v-if="confirming === 'permanent'">
            This runs "{{ description }}" now, and approves its action family for 24 hours — further
            requests of the same kind run without asking again.
          </template>
          <template v-else>
            This runs "{{ description }}" once, now.
          </template>
        </AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        <AlertDialogAction
          variant="destructive"
          @click="confirming && decide(confirming)"
        >
          {{ confirming === 'permanent' ? 'Approve for 24h' : 'Approve' }}
        </AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
