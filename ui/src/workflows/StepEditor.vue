<script setup lang="ts">
import { computed, ref } from "vue";
import { CircleHelp, Plus, Trash2, X } from "@lucide/vue";
import { parse, stringify } from "yaml";

import { Button } from "@/components/ui/button";
import { DurationInput } from "@/components/ui/duration-input";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { stepIcon } from "./step-icons";
import { addBranch, deleteBranch, renameBranch, earlierStepIDs, replaceStep, stepAt, workflowField, type StepPath, type StepRecord } from "./workflow-edit";
import type { WorkflowDefinitionEntry } from "@/stores/control-plane";
import type { InputSpec } from "./bind-event";
import { inputValue } from "./work-request";
import WorkflowPicker from "./WorkflowPicker.vue";
import { stepTitle } from "./workflow-graph";
import { settingsProblem, type SettingsSchema, type StepTypeInfo } from "./workflow-yaml";

const props = defineProps<{ yaml: string; path: StepPath; vocabulary: StepTypeInfo[]; workflows: WorkflowDefinitionEntry[] }>();
const emit = defineEmits<{ "update:yaml": [string]; close: []; remove: [] }>();

const step = computed<StepRecord>(() => stepAt(props.yaml, props.path) ?? {});
const type = computed(() => step.value.parallel ? "parallel" : typeof step.value.type === "string" ? step.value.type : "");
const branches = computed(() => Object.keys((step.value.parallel ?? {}) as Record<string, unknown>));
const newBranch = ref("");
const branchError = ref("");
function changeBranch(action: "add" | "rename" | "delete", name: string, next = ""): void {
  try {
    const source = action === "add" ? addBranch(props.yaml, props.path, name) : action === "rename" ? renameBranch(props.yaml, props.path, name, next) : deleteBranch(props.yaml, props.path, name);
    emit("update:yaml", source);
    newBranch.value = "";
    branchError.value = "";
  } catch (error) {
    branchError.value = String((error as Error).message);
  }
}
const info = computed(() => props.vocabulary.find((candidate) => candidate.name === type.value));
const settings = computed<Record<string, unknown>>(() =>
  typeof step.value.settings === "object" && step.value.settings !== null ? (step.value.settings as Record<string, unknown>) : {},
);
const earlier = computed(() => earlierStepIDs(props.yaml, props.path));
const problem = computed(() =>
  info.value?.settings && step.value.settings ? settingsProblem(step.value.settings, info.value.settings, "settings") : "",
);

type Kind = "workflow" | "select" | "text" | "line" | "boolean" | "number" | "lines" | "yaml";
const LEAD = ["mission", "workflow", "message", "body", "plan", "then", "status", "detail", "run", "rules"];
const longText = /mission|body|detail|plan|rules/;

function kindOf(key: string, schema: SettingsSchema): Kind {
  if ((["workflow.call", "workflow.handoff"].includes(type.value) && key === "workflow") || (type.value === "human.approve" && key === "then")) return "workflow";
  if (schema.type === "string") return schema.enum ? "select" : longText.test(key) ? "text" : "line";
  if (schema.type === "boolean") return "boolean";
  if (schema.type === "integer" || schema.type === "number") return "number";
  if (schema.type === "array" && schema.items?.type === "string") return "lines";
  return "yaml";
}

// The setting that says what the step does leads, on its own; choices and
// switches follow in a compact block; structured settings come last.
const fields = computed(() => {
  const entries = Object.entries(info.value?.settings?.properties ?? {})
    .filter(([key]) => type.value !== "workflow.call" || !["inputs", "outputs"].includes(key))
    .map(([key, schema]) => ({ key, schema, kind: kindOf(key, schema) }));
  const lead = entries.filter((field) => LEAD.includes(field.key) && field.kind !== "boolean").sort((a, b) => LEAD.indexOf(a.key) - LEAD.indexOf(b.key))[0];
  const rest = entries.filter((field) => field !== lead);
  return {
    lead,
    compact: rest.filter((field) => ["workflow", "select", "line", "number", "boolean"].includes(field.kind)),
    long: rest.filter((field) => ["text", "lines", "yaml"].includes(field.kind)),
  };
});

function write(next: StepRecord): void {
  // id and type lead, the way a step is read.
  const { id, type: stepType, ...rest } = next;
  emit("update:yaml", replaceStep(props.yaml, props.path, { id, type: stepType, ...rest }));
}
function setField(key: string, value: unknown): void {
  write({ ...step.value, [key]: value });
}
// Retry is one object: a field edit merges into it so attempts and backoff do
// not clobber each other, and clearing both drops the key.
function setRetry(next: { attempts?: number; backoff?: string }): void {
  setField("retry", { ...(step.value.retry as object), ...next });
}
function setSetting(key: string, value: unknown): void {
  const next = { ...settings.value, [key]: value };
  if (type.value === "workflow.call" && key === "wait" && value !== true) delete next.outputs;
  write({ ...step.value, settings: next });
}

function setTarget(key: string, value: string): void {
  const next = { ...settings.value, [key]: value };
  if (type.value === "workflow.call" && value !== settings.value[key]) {
    delete next.inputs;
    delete next.outputs;
  }
  write({ ...step.value, settings: next });
}
const callee = computed(() => props.workflows.find((entry) => entry.id === settings.value.workflow));
const callInputs = computed(() => Object.entries((workflowField(callee.value?.yaml ?? "", "inputs") ?? {}) as Record<string, InputSpec>));
const callOutputs = computed(() => Object.entries((workflowField(callee.value?.yaml ?? "", "outputs") ?? {}) as Record<string, InputSpec>));
const callerInputs = computed(() => Object.entries((workflowField(props.yaml, "inputs") ?? {}) as Record<string, InputSpec>));
const callerOutputs = computed(() => Object.entries((workflowField(props.yaml, "outputs") ?? {}) as Record<string, InputSpec>));
const inputMap = computed(() => (settings.value.inputs ?? {}) as Record<string, unknown>);
const outputMap = computed(() => (settings.value.outputs ?? {}) as Record<string, string>);
const isReference = (value: unknown) => typeof value === "string" && value.startsWith("inputs.");
const inputText = (name: string) => inputMap.value[name] === undefined ? "" : String(inputMap.value[name]);
function setCallInput(name: string, value: string, inputType?: string): void {
  setSetting("inputs", { ...inputMap.value, [name]: value === "" ? undefined : isReference(value) ? value : inputValue(value, inputType ?? "string") });
}

// A reference goes into the text setting last focused: that is how this step
// is connected to an earlier step's result.
const focused = ref<string | null>(null);
function reference(id: string, field: "summary" | "result"): void {
  const key = focused.value ?? fields.value.lead?.key;
  if (!key) return;
  const current = typeof settings.value[key] === "string" ? (settings.value[key] as string) : "";
  const token = `{{ steps.${id}.${field} }}`;
  setSetting(key, current ? `${current.trimEnd()} ${token}` : token);
}

const yamlErrors = ref<Record<string, string>>({});
function setYamlSetting(key: string, text: string): void {
  try {
    setSetting(key, text.trim() ? parse(text) : undefined);
    yamlErrors.value = { ...yamlErrors.value, [key]: "" };
  } catch (error) {
    yamlErrors.value = { ...yamlErrors.value, [key]: String((error as Error).message).split("\n")[0] };
  }
}
const asYaml = (value: unknown) => (value === undefined ? "" : stringify(value).trimEnd());
const label = (key: string, schema: SettingsSchema) =>
  schema.title ?? key.replaceAll("_", " ").replace(/^./, (c) => c.toUpperCase());
const text = (key: string) => (typeof settings.value[key] === "string" ? (settings.value[key] as string) : "");
</script>

<template>
  <aside class="flex h-full w-[24rem] flex-col border-l border-border bg-card shadow-xl">
    <header class="flex items-center gap-2.5 border-b border-border px-4 py-3">
      <span class="flex size-8 shrink-0 items-center justify-center rounded-md bg-secondary text-primary">
        <component :is="stepIcon(type)" class="size-4" />
      </span>
      <div class="min-w-0 flex-1">
        <input
          :value="(step.id as string) ?? ''"
          class="w-full truncate bg-transparent text-sm font-medium outline-none placeholder:text-fg-subtle focus:underline"
          :placeholder="stepTitle(type)"
          aria-label="Step id"
          @input="setField('id', ($event.target as HTMLInputElement).value)"
        />
        <div class="truncate font-mono text-[11px] text-fg-subtle">{{ type }}</div>
      </div>
      <Button type="button" variant="ghost" size="icon-sm" aria-label="Close" @click="emit('close')"><X /></Button>
    </header>

    <div class="flex-1 space-y-5 overflow-y-auto px-4 py-4">
      <p v-if="problem" class="rounded-md bg-danger/10 px-2.5 py-1.5 text-xs text-danger" role="alert">{{ problem }}</p>

      <section v-if="type === 'parallel'" class="space-y-3">
        <div class="text-xs font-medium text-muted-foreground">Branches</div>
        <div v-for="name in branches" :key="name" class="flex items-center gap-2">
          <Input :model-value="name" :aria-label="`Branch ${name}`" @change="changeBranch('rename', name, ($event.target as HTMLInputElement).value.trim())" />
          <Button type="button" variant="ghost" size="icon-sm" :aria-label="`Remove branch ${name}`" :disabled="branches.length <= 2" title="Parallel needs at least two branches" @click="changeBranch('delete', name)"><Trash2 /></Button>
        </div>
        <div class="flex items-center gap-2">
          <Input v-model="newBranch" aria-label="New branch name" placeholder="Branch name" @keydown.enter.prevent="changeBranch('add', newBranch.trim())" />
          <Button type="button" size="icon-sm" variant="outline" aria-label="Add branch" :disabled="!newBranch.trim()" @click="changeBranch('add', newBranch.trim())"><Plus /></Button>
        </div>
        <p v-if="branchError" role="alert" class="text-xs text-danger">{{ branchError }}</p>
      </section>

      <section v-if="fields.lead" class="space-y-1.5">
        <div class="flex items-center gap-1 text-xs font-medium text-muted-foreground" :title="fields.lead.schema.description">
          {{ label(fields.lead.key, fields.lead.schema) }}
          <CircleHelp v-if="fields.lead.schema.description" class="size-3 text-fg-subtle" />
        </div>
        <WorkflowPicker v-if="fields.lead.kind === 'workflow'" :value="text(fields.lead.key)" :workflows="workflows" :label="label(fields.lead.key, fields.lead.schema)" @pick="setTarget(fields.lead.key, $event)" />
        <select
          v-else-if="fields.lead.kind === 'select'"
          class="h-9 w-full rounded-md border border-input bg-background px-2"
          :value="text(fields.lead.key)"
          @change="setSetting(fields.lead.key, ($event.target as HTMLSelectElement).value || undefined)"
        >
          <option value="">Default</option>
          <option v-for="option in fields.lead.schema.enum" :key="option" :value="option">{{ option }}</option>
        </select>
        <Textarea
          v-else-if="fields.lead.kind === 'text'"
          :model-value="text(fields.lead.key)"
          class="min-h-40 text-[13px] leading-relaxed"
          @focus="focused = fields.lead.key"
          @update:model-value="setSetting(fields.lead.key, String($event))"
        />
        <Input
          v-else
          :model-value="text(fields.lead.key)"
          @focus="focused = fields.lead.key"
          @update:model-value="setSetting(fields.lead.key, String($event))"
        />
        <div v-if="earlier.length && fields.lead.kind !== 'select'" class="flex flex-wrap items-center gap-1 pt-0.5">
          <span class="text-[11px] text-fg-subtle">Insert</span>
          <template v-for="id in earlier" :key="id">
            <button type="button" class="rounded-full border border-border px-2 py-0.5 font-mono text-[11px] hover:border-primary hover:text-primary" @click="reference(id, 'summary')">{{ id }}</button>
            <button type="button" class="rounded-full border border-border px-2 py-0.5 font-mono text-[11px] hover:border-primary hover:text-primary" @click="reference(id, 'result')">{{ id }}.result</button>
          </template>
        </div>
      </section>

      <section v-if="fields.compact.length" class="divide-y divide-border rounded-md border border-border">
        <div v-for="field in fields.compact" :key="field.key" class="flex min-h-10 items-center gap-3 px-3 py-1.5">
          <span class="flex flex-1 items-center gap-1 text-[13px]" :title="field.schema.description">
            {{ label(field.key, field.schema) }}
            <CircleHelp v-if="field.schema.description" class="size-3 text-fg-subtle" />
          </span>
          <WorkflowPicker v-if="field.kind === 'workflow'" class="max-w-48" :value="text(field.key)" :workflows="workflows" :label="label(field.key, field.schema)" @pick="setTarget(field.key, $event)" />
          <Switch
            v-else-if="field.kind === 'boolean'"
            :model-value="settings[field.key] === true"
            @update:model-value="setSetting(field.key, $event || undefined)"
          />
          <select
            v-else-if="field.kind === 'select'"
            class="h-7 max-w-40 rounded-md border border-input bg-background px-1.5 text-[13px]"
            :value="text(field.key)"
            @change="setSetting(field.key, ($event.target as HTMLSelectElement).value || undefined)"
          >
            <option value="">Default</option>
            <option v-for="option in field.schema.enum" :key="option" :value="option">{{ option }}</option>
          </select>
          <input
            v-else-if="field.kind === 'number'"
            type="number"
            class="h-7 w-24 rounded-md border border-input bg-background px-2 text-right text-[13px]"
            :value="settings[field.key] ?? ''"
            @input="setSetting(field.key, ($event.target as HTMLInputElement).value === '' ? undefined : Number(($event.target as HTMLInputElement).value))"
          />
          <input
            v-else
            class="h-7 w-40 rounded-md border border-input bg-background px-2 text-[13px]"
            :value="text(field.key)"
            @focus="focused = field.key"
            @input="setSetting(field.key, ($event.target as HTMLInputElement).value)"
          />
        </div>
      </section>

      <template v-if="type === 'workflow.call' && callee">
        <section v-if="callInputs.length" class="space-y-3">
          <div class="text-xs font-medium text-muted-foreground">Inputs</div>
          <div v-for="[name, spec] in callInputs" :key="name" class="space-y-1.5">
            <div class="text-[13px]">{{ name }}<span v-if="spec.required" class="text-danger"> *</span><span class="ml-2 text-xs text-fg-subtle">{{ spec.type }}</span></div>
            <select :aria-label="`${name} source`" class="h-8 w-full rounded-md border border-input bg-background px-2 text-[13px]" :value="isReference(inputMap[name]) || ['object', 'array'].includes(spec.type ?? '') ? 'input' : 'value'" @change="setCallInput(name, ($event.target as HTMLSelectElement).value === 'input' ? 'inputs.' : '')">
              <option v-if="!['object', 'array'].includes(spec.type ?? '')" value="value">Value</option>
              <option value="input">Caller input</option>
            </select>
            <select v-if="isReference(inputMap[name]) || ['object', 'array'].includes(spec.type ?? '')" :aria-label="name" class="h-8 w-full rounded-md border border-input bg-background px-2 text-[13px]" :value="inputText(name)" @change="setCallInput(name, ($event.target as HTMLSelectElement).value)">
              <option value="">Choose input</option>
              <option v-for="[key] in callerInputs.filter(([, input]) => input.type === spec.type || input.type === 'any' || spec.type === 'any')" :key="key" :value="`inputs.${key}`">{{ key }}</option>
            </select>
            <select v-else-if="spec.type === 'bool'" :aria-label="name" class="h-8 w-full rounded-md border border-input bg-background px-2 text-[13px]" :value="inputText(name)" @change="setCallInput(name, ($event.target as HTMLSelectElement).value, spec.type)">
              <option value="">Unset</option><option value="true">True</option><option value="false">False</option>
            </select>
            <Input v-else :aria-label="name" :type="spec.type === 'number' ? 'number' : 'text'" step="any" :model-value="inputText(name)" @update:model-value="setCallInput(name, String($event), spec.type)" />
          </div>
        </section>
        <section v-if="callOutputs.length" class="space-y-3">
          <div class="text-xs font-medium text-muted-foreground">Outputs</div>
          <div v-for="[name, spec] in callOutputs" :key="name" class="space-y-1.5">
            <div class="text-[13px]">{{ name }}<span class="ml-2 text-xs text-fg-subtle">{{ spec.type }}</span></div>
            <select :aria-label="`${name} output`" :disabled="settings.wait !== true" class="h-8 w-full rounded-md border border-input bg-background px-2 text-[13px]" :value="outputMap[name] ?? ''" @change="setSetting('outputs', { ...outputMap, [name]: ($event.target as HTMLSelectElement).value || undefined })">
              <option value="">Do not publish</option>
              <option v-for="[key] in callerOutputs.filter(([, output]) => output.type === spec.type || spec.type === 'any' || output.type === 'any')" :key="key" :value="`outputs.${key}`">{{ key }}</option>
            </select>
          </div>
        </section>
      </template>

      <section v-for="field in fields.long" :key="field.key" class="space-y-1.5">
        <div class="flex items-center gap-1 text-xs font-medium text-muted-foreground" :title="field.schema.description">
          {{ label(field.key, field.schema) }}
          <CircleHelp v-if="field.schema.description" class="size-3 text-fg-subtle" />
        </div>
        <Textarea
          v-if="field.kind === 'text'"
          :model-value="text(field.key)"
          class="min-h-20 text-[13px]"
          @focus="focused = field.key"
          @update:model-value="setSetting(field.key, String($event))"
        />
        <Textarea
          v-else-if="field.kind === 'lines'"
          :model-value="Array.isArray(settings[field.key]) ? (settings[field.key] as string[]).join('\n') : ''"
          class="min-h-16 font-mono text-xs"
          placeholder="One per line"
          @update:model-value="setSetting(field.key, String($event).split('\n').filter(Boolean))"
        />
        <template v-else>
          <Textarea
            :model-value="asYaml(settings[field.key])"
            class="min-h-24 font-mono text-xs"
            placeholder="YAML"
            @change="setYamlSetting(field.key, ($event.target as HTMLTextAreaElement).value)"
          />
          <span v-if="yamlErrors[field.key]" class="text-xs text-danger">{{ yamlErrors[field.key] }}</span>
        </template>
      </section>

      <section class="space-y-2">
        <div class="text-xs font-medium text-muted-foreground">Flow</div>
        <div class="divide-y divide-border rounded-md border border-border">
          <label class="flex min-h-10 items-center gap-3 px-3 py-1.5">
            <span class="flex-1 text-[13px]">Run only when</span>
            <input
              class="h-7 w-48 rounded-md border border-input bg-background px-2 font-mono text-xs"
              placeholder="always"
              :value="(step.when as string) ?? ''"
              @input="setField('when', ($event.target as HTMLInputElement).value)"
            />
          </label>
          <label class="flex min-h-10 items-center gap-3 px-3 py-1.5">
            <span class="flex-1 text-[13px]">If it fails</span>
            <select
              class="h-7 rounded-md border border-input bg-background px-1.5 text-[13px]"
              :value="(step.on_failure as string) ?? ''"
              @change="setField('on_failure', ($event.target as HTMLSelectElement).value || undefined)"
            >
              <option value="">Park the task</option>
              <option value="continue">Continue</option>
            </select>
          </label>
          <label class="flex min-h-10 items-center gap-3 px-3 py-1.5">
            <span class="flex-1 text-[13px]">Attempts</span>
            <input
              type="number"
              min="1"
              max="10"
              placeholder="1"
              class="h-7 w-20 rounded-md border border-input bg-background px-2 text-right text-[13px]"
              :value="(step.retry as { attempts?: number } | undefined)?.attempts ?? ''"
              @input="setRetry({ attempts: ($event.target as HTMLInputElement).value === '' ? undefined : Number(($event.target as HTMLInputElement).value) })"
            />
          </label>
          <label class="flex min-h-10 items-center gap-3 px-3 py-1.5">
            <span class="flex-1 text-[13px]">Retry backoff</span>
            <DurationInput
              :model-value="(step.retry as { backoff?: string } | undefined)?.backoff ?? ''"
              :units="['s', 'm', 'h']"
              @update:model-value="setRetry({ backoff: $event || undefined })"
            />
          </label>
        </div>
      </section>
    </div>

    <footer class="flex items-center border-t border-border px-4 py-2.5">
      <Button type="button" variant="ghost" size="sm" class="text-danger" @click="emit('remove')"><Trash2 /> Delete step</Button>
      <Button type="button" size="sm" class="ml-auto" @click="emit('close')">Done</Button>
    </footer>
  </aside>
</template>
