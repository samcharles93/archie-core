<script setup lang="ts">
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { actionErrorTitle, type ActionFailure } from "./state";

/**
 * One failed operator action. The title comes from the classified kind, so an
 * expired session reads differently from a rejected one; the message under it
 * is archied's own, which is the part an operator can act on.
 */
defineProps<{ failure: ActionFailure }>();
</script>

<template>
  <Alert variant="destructive">
    <AlertTitle>{{ actionErrorTitle(failure.kind) }}</AlertTitle>
    <AlertDescription>{{ failure.message }}</AlertDescription>
  </Alert>
</template>
