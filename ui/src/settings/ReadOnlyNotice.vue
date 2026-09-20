<script setup lang="ts">
import { LockKeyhole } from "@lucide/vue";

import { Alert, AlertTitle } from "@/components/ui/alert";
import { configEditable } from "./state";

/**
 * Says so when this process cannot write configuration.
 *
 * A process that only renders a published snapshot has no update path, so
 * every editable row on the page is a value and the PATCH routes answer 503.
 * A page that simply showed no controls would leave the reader guessing
 * whether a setting is immutable, read-only here, or broken -- so it states
 * which (archie-core-ymut).
 */
</script>

<template>
  <Alert v-if="!configEditable" class="mb-4">
    <LockKeyhole />
    <AlertTitle>Read-only settings</AlertTitle>
  </Alert>
</template>
