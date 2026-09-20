<script setup lang="ts">
import { computed } from "vue";

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

function defer(): void {
  void deferUpdate(update.value?.snapshot);
}

function install(): void {
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
           says it can perform one. -->
      <Button v-if="update.can_install" @click="install">Install update</Button>
    </div>
  </ConfigCard>
</template>
