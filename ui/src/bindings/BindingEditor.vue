<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import {
  draftFromBinding,
  emptyDraft,
  type Binding,
  type BindingDraft,
  type MappingOption,
  type WorkflowOption,
} from "./binding-draft";

/**
 * The binding editor: name, the matcher source senders POST to, the field
 * mapping, the workflow, an optional repo pin, and the shared secret their
 * signature is checked against.
 */

const props = defineProps<{
  /** null is a new binding; a binding is an edit. */
  binding: Binding | null;
  mappings: MappingOption[];
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
    if (isOpen) draft.value = props.binding ? draftFromBinding(props.binding) : emptyDraft();
  },
  { immediate: true },
);

const title = computed(() => (props.binding ? "Edit binding" : "New binding"));
const secretHint = computed(() =>
  props.binding
    ? "Leave blank to keep the current secret."
    : "Senders sign with this via HMAC-SHA256.",
);
</script>

<template>
  <Dialog v-model:open="open">
    <!-- Seven fields make this taller than a short window; the dialog itself
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
            <FieldLabel for="binding-source">Source</FieldLabel>
            <Input id="binding-source" v-model="draft.source" />
            <FieldDescription>The path segment senders POST to, for example "sentry".</FieldDescription>
          </Field>

          <Field>
            <FieldLabel for="binding-mapping">Field mapping</FieldLabel>
            <Select v-model="draft.mappingId">
              <SelectTrigger id="binding-mapping" class="w-full">
                <SelectValue placeholder="Pick a saved mapping" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem v-for="mapping in props.mappings" :key="mapping.id" :value="String(mapping.id)">
                    {{ mapping.name }}
                  </SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
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

          <Field>
            <FieldLabel for="binding-secret">Shared secret</FieldLabel>
            <Input id="binding-secret" v-model="draft.secret" type="password" autocomplete="off" />
            <FieldDescription>{{ secretHint }}</FieldDescription>
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
