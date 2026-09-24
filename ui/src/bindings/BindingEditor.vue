<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { eventTypeLabel, type EventType } from "@/captures/event-types";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import {
  draftFromBinding,
  emptyDraft,
  mappingsForEventType,
  type Binding,
  type BindingDraft,
  type MappingOption,
  type WorkflowOption,
} from "./binding-draft";

/**
 * The binding editor: name, the event type it applies to, one of that type's
 * mappings, an optional filter over the mapping's parameters, the workflow and
 * an optional repo pin. Signing is the source's.
 */

const props = defineProps<{
  /** null is a new binding; a binding is an edit. */
  binding: Binding | null;
  mappings: MappingOption[];
  eventTypes: EventType[];
  workflows: WorkflowOption[];
  saving: boolean;
  error: string | null;
}>();

const open = defineModel<boolean>("open", { required: true });
const emit = defineEmits<{ save: [draft: BindingDraft] }>();

// The draft is re-seeded on every open, so an edit that was cancelled -- or a
// save that failed -- never leaks into the next binding's form.
const draft = ref<BindingDraft>(emptyDraft());
watch(
  open,
  (isOpen) => {
    if (isOpen) draft.value = props.binding ? draftFromBinding(props.binding, props.mappings) : emptyDraft();
  },
  { immediate: true },
);

const typeMappings = computed(() => mappingsForEventType(props.mappings, draft.value.eventTypeId));

// A mapping belongs to one event type, so changing the type clears a mapping
// that no longer fits it.
watch(
  () => draft.value.eventTypeId,
  () => {
    if (!typeMappings.value.some((m) => m.id === draft.value.mappingId)) draft.value.mappingId = "";
  },
);

const title = computed(() => (props.binding ? "Edit binding" : "New binding"));
</script>

<template>
  <Dialog v-model:open="open">
    <!-- Six fields make this taller than a short window; the dialog itself
         scrolls rather than clipping its own Save button. -->
    <DialogContent class="max-h-[85vh] overflow-y-auto sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>
          A binding is not armed until it is approved, and editing one drops it back to pending approval.
        </DialogDescription>
      </DialogHeader>

      <form @submit.prevent="emit('save', draft)">
        <FieldGroup>
          <Field>
            <FieldLabel for="binding-name">Name</FieldLabel>
            <Input id="binding-name" v-model="draft.name" />
          </Field>

          <Field>
            <FieldLabel for="binding-event-type">Event type</FieldLabel>
            <Select v-model="draft.eventTypeId">
              <SelectTrigger id="binding-event-type" class="w-full">
                <SelectValue placeholder="Pick an event type" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem v-for="type in props.eventTypes" :key="type.id" :value="type.id">
                    {{ eventTypeLabel(type.id, props.eventTypes) }}
                  </SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </Field>

          <Field>
            <FieldLabel for="binding-mapping">Field mapping</FieldLabel>
            <Select v-model="draft.mappingId" :disabled="!typeMappings.length">
              <SelectTrigger id="binding-mapping" class="w-full">
                <SelectValue :placeholder="draft.eventTypeId && !typeMappings.length ? 'No mappings for this event type' : 'Pick a mapping'" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem v-for="mapping in typeMappings" :key="mapping.id" :value="String(mapping.id)">
                    {{ mapping.name }}
                  </SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </Field>

          <Field>
            <FieldLabel for="binding-filter">Filter</FieldLabel>
            <Input id="binding-filter" v-model="draft.filter" class="font-mono text-xs" placeholder='severity in ["high", "critical"]' />
            <FieldDescription>Optional CEL over the mapping's parameters.</FieldDescription>
          </Field>

          <Field>
            <FieldLabel for="binding-workflow">Workflow</FieldLabel>
            <Select v-model="draft.workflow">
              <SelectTrigger id="binding-workflow" class="w-full">
                <SelectValue placeholder="Pick a workflow" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem v-for="workflow in props.workflows" :key="workflow.id" :value="workflow.id">
                    {{ workflow.name }}
                  </SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </Field>

          <Field>
            <div class="grid grid-cols-2 items-start gap-3">
              <Field>
                <FieldLabel for="binding-owner">Pinned owner</FieldLabel>
                <Input id="binding-owner" v-model="draft.owner" />
              </Field>
              <Field>
                <FieldLabel for="binding-repo">Pinned repo</FieldLabel>
                <Input id="binding-repo" v-model="draft.repo" />
              </Field>
            </div>
            <FieldDescription>
              Optional. A pin is both halves or neither, so leave both blank to use the single configured repo.
            </FieldDescription>
          </Field>

        </FieldGroup>

        <!-- Beside the buttons, not at the top: the dialog scrolls, and Save is
             clicked at the bottom, where a refusal must be seen. -->
        <Alert v-if="props.error" variant="destructive" class="mt-4">
          <AlertTitle>Could not save</AlertTitle>
          <AlertDescription>{{ props.error }}</AlertDescription>
        </Alert>

        <DialogFooter class="mt-4">
          <Button type="button" variant="outline" @click="open = false">Cancel</Button>
          <Button type="submit" :disabled="props.saving">
            <Spinner v-if="props.saving" data-icon="inline-start" />
            {{ props.saving ? "Saving" : "Save" }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
