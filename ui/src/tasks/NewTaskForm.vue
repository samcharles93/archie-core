<script setup lang="ts">
import { computed, onMounted, ref, useId, watchEffect } from "vue";
import { ChevronRight } from "@lucide/vue";
import { RadioGroupItem, RadioGroupRoot } from "reka-ui";

import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Kbd } from "@/components/ui/kbd";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import { delivered } from "@/lib/delivered";
import { useIdentitiesStore } from "@/stores/identities";
import type { WorkflowDefinition, WorkflowStats } from "@/workflows/workflow-rows";

/** Starting work enters Archie's normal admitted task queue, not a side door. */
const emit = defineEmits<{ started: [taskId: number]; cancel: [] }>();
const uid = useId();

const definitions = ref<WorkflowDefinition[]>([]);
const stats = ref<WorkflowStats[]>([]);
const recentRepos = ref<string[]>([]);
const loadError = ref("");
onMounted(async () => {
  try {
    const [workflows, tasks] = await Promise.all([
      api.workflows<{ definitions?: WorkflowDefinition[]; workflows?: WorkflowStats[] }>(),
      api.tasks<{ owner?: string; repo?: string }[]>(),
    ]);
    definitions.value = (workflows?.definitions ?? []).filter((d) => d.enabled);
    stats.value = workflows?.workflows ?? [];
    recentRepos.value = [...new Set((tasks ?? []).filter((t) => t.owner && t.repo).map((t) => `${t.owner}/${t.repo}`))].slice(0, 5);
  } catch (err) {
    loadError.value = String((err as Error).message || err);
  }
});

const identities = useIdentitiesStore();
identities.watch();
const actors = computed(() => identities.identities.filter((i) => i.kind !== "system" && i.lifecycle === "active"));

const repository = ref("");
const workflow = ref("");
const title = ref("");
const instructions = ref("");
const identity = ref("");
watchEffect(() => {
  if (!workflow.value && definitions.value[0]) workflow.value = definitions.value[0].id;
  if (!identity.value && actors.value[0]) identity.value = actors.value[0].id;
});

const statFor = (id: string) => stats.value.find((s) => s.workflow === id);
const identityName = computed(() => actors.value.find((a) => a.id === identity.value)?.display_name ?? "nobody");

const submitting = ref(false);
const error = ref("");
const ready = computed(
  () => !!(repository.value.trim() && workflow.value && title.value.trim() && instructions.value.trim() && identity.value),
);
async function submit() {
  if (!ready.value || submitting.value) return;
  submitting.value = true;
  error.value = "";
  try {
    const result = await api.workRequest<{ task_id: number }>({
      identity: identity.value,
      repository: repository.value.trim(),
      workflow: workflow.value,
      title: title.value.trim(),
      instructions: instructions.value,
    });
    emit("started", result.task_id);
  } catch (err) {
    error.value = String((err as Error).message || err);
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <form class="grid gap-5" @submit.prevent="submit" @keydown.meta.enter.prevent="submit" @keydown.ctrl.enter.prevent="submit">
    <p v-if="loadError" class="text-sm text-danger" role="alert">{{ loadError }}</p>

    <div class="grid gap-1.5">
      <label :for="`${uid}-repo`" class="text-[13px] font-medium">Repository</label>
      <Input :id="`${uid}-repo`" v-model="repository" class="font-mono" placeholder="owner/repository" :list="`${uid}-repos`" required />
      <datalist :id="`${uid}-repos`">
        <option v-for="r in recentRepos" :key="r" :value="r" />
      </datalist>
      <p v-if="recentRepos.length" class="text-xs text-fg-subtle">
        Recent:
        <button
          v-for="r in recentRepos"
          :key="r"
          type="button"
          class="mr-2 font-mono hover:text-foreground"
          @click="repository = r"
        >{{ r }}</button>
      </p>
    </div>

    <div class="grid gap-1.5">
      <span :id="`${uid}-wf`" class="text-[13px] font-medium">Workflow</span>
      <RadioGroupRoot v-model="workflow" :aria-labelledby="`${uid}-wf`" class="grid grid-cols-1 gap-2 sm:grid-cols-3" loop>
        <RadioGroupItem
          v-for="d in definitions"
          :key="d.id"
          :value="d.id"
          class="rounded-md border border-border bg-card px-3 py-2.5 text-left transition-colors hover:bg-secondary focus-visible:outline-2 focus-visible:outline-ring data-[state=checked]:border-primary data-[state=checked]:bg-secondary"
        >
          <span class="block truncate font-mono text-[13px] font-medium">{{ d.name || d.id }}</span>
          <span class="block text-xs text-fg-subtle">
            {{ statFor(d.id)?.runs || 0 }} runs<template v-if="statFor(d.id)?.runs">
              · {{ Math.round((delivered(statFor(d.id)) / (statFor(d.id)?.runs || 1)) * 100) }}% delivered</template
            >
          </span>
        </RadioGroupItem>
      </RadioGroupRoot>
      <p v-if="!definitions.length && !loadError" class="text-xs text-fg-subtle">No enabled workflows.</p>
    </div>

    <div class="grid gap-1.5">
      <label :for="`${uid}-title`" class="text-[13px] font-medium">Title</label>
      <Input :id="`${uid}-title`" v-model="title" required />
    </div>
    <div class="grid gap-1.5">
      <label :for="`${uid}-instr`" class="text-[13px] font-medium">Instructions</label>
      <Textarea :id="`${uid}-instr`" v-model="instructions" :rows="4" required />
    </div>

    <Collapsible>
      <CollapsibleTrigger class="group flex items-center gap-1 text-xs text-fg-muted hover:text-foreground">
        <ChevronRight class="size-3.5 transition-transform group-data-[state=open]:rotate-90" aria-hidden="true" />
        Advanced · run as <span class="font-mono">{{ identityName }}</span>
      </CollapsibleTrigger>
      <CollapsibleContent class="pt-2">
        <Select v-model="identity">
          <SelectTrigger class="w-64" aria-label="Run as identity"><SelectValue placeholder="Choose an identity" /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="a in actors" :key="a.id" :value="a.id">{{ a.display_name }}</SelectItem>
          </SelectContent>
        </Select>
      </CollapsibleContent>
    </Collapsible>

    <p v-if="error" class="text-sm text-danger" role="alert">{{ error }}</p>
    <div class="flex items-center gap-2 border-t border-border pt-4">
      <span class="mr-auto text-xs text-fg-subtle"><Kbd>⌘</Kbd> <Kbd>↵</Kbd> to start</span>
      <Button type="button" variant="ghost" @click="emit('cancel')">Cancel</Button>
      <Button type="submit" :disabled="!ready || submitting"><Spinner v-if="submitting" data-icon="inline-start" /> Start task</Button>
    </div>
  </form>
</template>
