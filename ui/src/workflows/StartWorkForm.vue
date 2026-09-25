<script setup lang="ts">
import { computed, ref, useId, watchEffect } from "vue";

import { Button } from "@/components/ui/button";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import { workflowLabel } from "./labels";

/** A workflow definition, as far as this form needs it. */
interface Definition {
  id: string;
  name?: string;
  enabled?: boolean;
}

const props = defineProps<{ definitions: Definition[] }>();
const uid = useId();
const emit = defineEmits<{ started: [taskId: number] }>();

/** Starting work enters Archie's normal admitted task queue, not a side door. */

const enabled = computed(() => props.definitions.filter((d) => d.enabled));
const defaultWorkflow = computed(() => enabled.value[0]?.id ?? "");

const identity = ref("");
const repository = ref("");
const workflow = ref(defaultWorkflow.value);
const title = ref("");
const instructions = ref("");
const submitting = ref(false);
const notice = ref("");

// The definitions arrive after the first render, so the default can only be
// applied once they do.
watchEffect(() => {
  if (!workflow.value && defaultWorkflow.value)
    workflow.value = defaultWorkflow.value;
});

async function submit() {
  submitting.value = true;
  notice.value = "";
  try {
    const result = await api.workRequest<{ task_id: number }>({
      identity: identity.value,
      repository: repository.value,
      workflow: workflow.value,
      title: title.value,
      instructions: instructions.value,
    });
    notice.value = `Queued task #${result.task_id}.`;
    emit("started", result.task_id);
    identity.value = "";
    repository.value = "";
    workflow.value = defaultWorkflow.value;
    title.value = "";
    instructions.value = "";
  } catch (err) {
    notice.value = String((err as Error).message || err);
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <form @submit.prevent="submit">
    <FieldGroup>
      <Field>
        <FieldLabel :for="`${uid}-identity`">Identity</FieldLabel>
        <Input :id="`${uid}-identity`" v-model="identity" required />
      </Field>
      <Field>
        <FieldLabel :for="`${uid}-repository`">Repository</FieldLabel>
        <Input
          :id="`${uid}-repository`"
          v-model="repository"
          placeholder="owner/repository"
          required
        />
      </Field>
      <Field>
        <FieldLabel :for="`${uid}-workflow`">Workflow</FieldLabel>
        <Select v-model="workflow" required>
          <SelectTrigger :id="`${uid}-workflow`">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem v-for="d in enabled" :key="d.id" :value="d.id">
                {{ workflowLabel(d.name || d.id) }}
              </SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </Field>
      <Field>
        <FieldLabel :for="`${uid}-title`">Task title</FieldLabel>
        <Input :id="`${uid}-title`" v-model="title" required />
      </Field>
      <Field>
        <FieldLabel :for="`${uid}-instructions`">Instructions</FieldLabel>
        <Textarea
          :id="`${uid}-instructions`"
          v-model="instructions"
          :rows="3"
          required
        />
      </Field>
      <Field orientation="horizontal">
        <Button type="submit" :disabled="submitting">Start work</Button>
        <p v-if="notice" class="text-sm text-fg-muted">{{ notice }}</p>
      </Field>
    </FieldGroup>
  </form>
</template>
