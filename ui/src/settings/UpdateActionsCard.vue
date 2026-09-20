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
import ConfigCard from "./ConfigCard.vue";
import { deferUpdate, installUpdate, update } from "./state";

/**
 * The actionable half of update checking: what is available, and the two
 * decisions you can make about it. The read-only comparison of components and
 * versions is UpdateStatusCard.
 *
 * A deployment that did not wire update checking answers 501, which leaves
 * `update` null and this card absent rather than in an error state
 * (archie-core-tf20).
 */
const available = computed(() => update.value?.available ?? []);

const summary = computed(() => {
  const data = update.value;
  if (data?.error) return data.error;
  if (data?.snapshot?.deferred) return "Update deferred.";
  return "Archie is up to date.";
});

// Installing restarts archied, so it is confirmed first: the operator needs
// to know that this dashboard, and any tasks archied is running, go down with
// it before the install call fires.
const confirmingInstall = ref(false);

function defer(): void {
  void deferUpdate(update.value?.snapshot);
}

function onInstallOpen(open: boolean): void {
  if (!open) confirmingInstall.value = false;
}

function install(): void {
  confirmingInstall.value = false;
  void installUpdate(update.value?.snapshot);
}
</script>

<template>
  <ConfigCard v-if="update" title="Updates" description="Whether a newer release is available, and what to do about it.">
    <p class="text-sm text-fg-muted">
      <template v-if="available.length">
        {{ available.map((c) => `${c.Label || c.label}: ${c.Available || c.available}`).join(" · ") }}
      </template>
      <template v-else>{{ summary }}</template>
    </p>
    <div v-if="available.length" class="mt-3 flex flex-wrap gap-2">
      <Button variant="outline" @click="defer">Defer</Button>
      <!-- Installing restarts archied, so it is offered only when the server
           says it can perform one, and confirmed before it runs. -->
      <Button v-if="update.can_install" variant="destructive" @click="confirmingInstall = true">Install update</Button>
    </div>

    <AlertDialog :open="confirmingInstall" @update:open="onInstallOpen">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Install update</AlertDialogTitle>
          <AlertDialogDescription>
            Installing restarts archied. This dashboard will be unavailable until it comes back, and
            any tasks archied is running are interrupted.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" @click="install">Install update</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </ConfigCard>
</template>
