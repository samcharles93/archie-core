<script setup lang="ts">
import { computed } from "vue";

import ConfigCard from "./ConfigCard.vue";
import DangerRollbackRequest from "./DangerRollbackRequest.vue";
import DangerStopRequest from "./DangerStopRequest.vue";
import DangerousActionRow from "./DangerousActionRow.vue";
import { dangerous } from "./state";

/**
 * Requests and adjudicates the actions that need approval.
 *
 * These controls used to sit in the chat panel, which is where they were first
 * built; installing an update and approving a dangerous action are things you
 * do to the deployment, not things you say to Archie, so they moved here with
 * Configuration's other operator controls (archie-core-tf20).
 *
 * Nothing here acts on its own: a request only queues an approval, and the
 * decision buttons are the only thing that lets one run. A deployment that did
 * not wire the capability answers 501, which leaves `dangerous` null and the
 * card absent rather than in an error state.
 */
const pending = computed(() => dangerous.value?.pending ?? []);
const checkpoints = computed(() => dangerous.value?.checkpoints ?? []);
const error = computed(() => dangerous.value?.error ?? "");
</script>

<template>
  <ConfigCard
    v-if="dangerous"
    title="Dangerous actions"
    :description="
      error
        ? 'Actions that need explicit approval before they run.'
        : 'Actions that need explicit approval before they run. Requesting one only queues it.'
    "
  >
    <p v-if="error" class="text-sm text-fg-muted">{{ error }}</p>
    <template v-else>
      <!-- Each request is its own row with its own input: they queue
           different kinds of approval and share nothing but the card. -->
      <div class="flex flex-col gap-3">
        <DangerRollbackRequest :checkpoints="checkpoints" />
        <DangerStopRequest />
      </div>
      <p v-if="!pending.length" class="mt-3 text-sm text-fg-muted">
        No pending dangerous actions.
      </p>
      <div v-else class="mt-3">
        <DangerousActionRow
          v-for="action in pending"
          :key="action.id"
          :action="action"
        />
      </div>
    </template>
  </ConfigCard>
</template>
