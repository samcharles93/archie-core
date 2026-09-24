<script setup lang="ts">
import { computed } from "vue";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ago } from "@/lib/format";
import { eventTypeLabel } from "@/captures/event-types";
import { captures, capturesEnabled, draft, eventTypes, selectCapture, type Capture } from "./state";

/**
 * The mapping's own fields: the name it is saved under, the organisational
 * source hint, and the captured event the payload and the preview resolve
 * against. The hint is a note to a human, never a matcher -- t2db.4 decides
 * which events a mapping applies to.
 *
 * Laid out as labelled controls rather than the FieldGroup/Field pair, which
 * this project has not installed; each label is bound to its control by id so
 * the association survives a click on either.
 */

/** The picker's value for "no capture chosen". Capture ids are numeric, so no
 * real option can collide with this one, and reka-ui refuses an empty-string
 * item value outright. */
const NO_CAPTURE = "none";

const captureValue = computed(() => (draft.value.captureId === null ? NO_CAPTURE : String(draft.value.captureId)));

function onCaptureChange(value: unknown): void {
  selectCapture(String(value));
}

function captureLabel(capture: Capture): string {
  return `${capture.source || "(unknown)"} — ${ago(capture.received_at)}`;
}

/** What an empty picker means, which is not the same on every deployment. */
const captureHint = computed(() => {
  if (captures.value.length) return null;
  return capturesEnabled.value
    ? "No captured events yet."
    : "Capture is not configured on this deployment.";
});
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="flex flex-col gap-2">
      <Label for="mapping-name">Name</Label>
      <Input id="mapping-name" v-model="draft.name" />
    </div>

    <div class="flex flex-col gap-2">
      <Label for="mapping-event-type">Event type</Label>
      <Select v-model="draft.eventTypeId">
        <SelectTrigger id="mapping-event-type" class="w-full">
          <SelectValue placeholder="Pick an event type" />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem v-for="type in eventTypes" :key="type.id" :value="type.id">
              {{ eventTypeLabel(type.id, eventTypes) }}
            </SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
    </div>

    <div class="flex flex-col gap-2">
      <Label for="mapping-source-hint">Source hint (organisational only)</Label>
      <Input id="mapping-source-hint" v-model="draft.sourceHint" />
    </div>

    <div class="flex flex-col gap-2">
      <Label for="mapping-capture">Preview against captured event</Label>
      <Select :model-value="captureValue" @update:model-value="onCaptureChange">
        <SelectTrigger id="mapping-capture" class="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem :value="NO_CAPTURE">— pick a captured event —</SelectItem>
            <SelectItem v-for="capture in captures" :key="capture.id" :value="String(capture.id)">
              {{ captureLabel(capture) }}
            </SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
      <p v-if="captureHint" class="text-xs text-fg-subtle">{{ captureHint }}</p>
    </div>
  </div>
</template>
