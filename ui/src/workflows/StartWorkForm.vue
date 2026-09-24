<script setup lang="ts">
import { computed, ref, watchEffect } from "vue";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
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
  <Card class="mb-4">
    <CardHeader>
      <CardTitle>Start work</CardTitle>
      <CardDescription
        >This enters Archie's normal admitted task queue.</CardDescription
      >
    </CardHeader>
    <CardContent>
      <form @submit.prevent="submit">
        <FieldGroup>
          <Field>
            <FieldLabel for="wf-identity">Identity</FieldLabel>
            <Input id="wf-identity" v-model="identity" required />
          </Field>
          <Field>
            <FieldLabel for="wf-repository">Repository</FieldLabel>
            <Input
              id="wf-repository"
              v-model="repository"
              placeholder="owner/repository"
              required
            />
          </Field>
          <Field>
            <FieldLabel for="wf-workflow">Workflow</FieldLabel>
            <Select v-model="workflow" required>
              <SelectTrigger id="wf-workflow">
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
            <FieldLabel for="wf-title">Task title</FieldLabel>
            <Input id="wf-title" v-model="title" required />
          </Field>
          <Field>
            <FieldLabel for="wf-instructions">Instructions</FieldLabel>
            <Textarea
              id="wf-instructions"
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
    </CardContent>
  </Card>
</template>
