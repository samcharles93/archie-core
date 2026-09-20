<script setup lang="ts">
import { computed, ref } from "vue";

import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { requestDangerous } from "./state";

/**
 * Queues a stop for a named process. Like every request here it only asks:
 * the approval decides whether it runs.
 */
const spec = ref("");
const requested = computed(() => spec.value.trim().length > 0);
</script>

<template>
  <Field orientation="horizontal">
    <FieldLabel class="sr-only" for="danger-stop">Process name or id</FieldLabel>
    <Input
      id="danger-stop"
      v-model="spec"
      placeholder="Process name or id"
      autocomplete="off"
      class="w-full min-[701px]:max-w-80"
    />
    <Button variant="outline" :disabled="!requested" @click="requestDangerous('stop', spec)">Request stop</Button>
  </Field>
</template>
