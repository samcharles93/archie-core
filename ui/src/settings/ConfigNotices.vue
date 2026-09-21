<script setup lang="ts">
import { computed } from "vue";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { config } from "./state";

/**
 * What the running configuration says about itself: whether the last reload was
 * applied. The running config is unchanged after a failed reload, so the notice
 * is a statement about state rather than a warning about the values below it.
 */
const lastError = computed(() => config.value?.reload?.last_error ?? "");
</script>

<template>
  <Alert v-if="lastError" class="mb-4">
    <AlertDescription>The last config reload failed; the running config is unchanged: {{ lastError }}</AlertDescription>
  </Alert>
</template>
