<script setup lang="ts">
import { ref } from "vue";

import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { requestDangerous } from "./state";
import type { DangerCheckpoint } from "./types";

/**
 * Queues a rollback to a checkpoint. The request only asks: an approval has to
 * be granted in the list below before anything runs.
 *
 * A checkpoint's number and label arrive under either casing depending on the
 * source, so both are read.
 */
const props = defineProps<{ checkpoints: DangerCheckpoint[] }>();

const checkpoint = ref("");

const number = (cp: DangerCheckpoint): number | undefined =>
  cp.Number ?? cp.number;
const label = (cp: DangerCheckpoint): string =>
  cp.Label ?? cp.label ?? "checkpoint";
</script>

<template>
  <Field orientation="horizontal">
    <FieldLabel class="sr-only" for="danger-checkpoint">Checkpoint</FieldLabel>
    <Select v-model="checkpoint">
      <SelectTrigger id="danger-checkpoint" class="w-full min-[701px]:max-w-80">
        <SelectValue placeholder="Select checkpoint" />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          <SelectItem
            v-for="cp in props.checkpoints"
            :key="String(number(cp))"
            :value="String(number(cp))"
          >
            {{ number(cp) }} — {{ label(cp) }}
          </SelectItem>
        </SelectGroup>
      </SelectContent>
    </Select>
    <Button
      variant="outline"
      :disabled="!checkpoint"
      @click="requestDangerous('rollback', checkpoint)"
    >
      Request rollback
    </Button>
  </Field>
</template>
