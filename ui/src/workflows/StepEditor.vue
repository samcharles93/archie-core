<script setup lang="ts">
import { computed, ref } from "vue";
import { CircleHelp, Trash2, X } from "@lucide/vue";
import { parse, stringify } from "yaml";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { stepIcon } from "./step-icons";
import { earlierStepIDs, replaceStep, stepAt, type StepPath, type StepRecord } from "./workflow-edit";
import { stepTitle } from "./workflow-graph";
import { settingsProblem, type SettingsSchema, type StepTypeInfo } from "./workflow-yaml";

const props = defineProps<{ yaml: string; path: StepPath; vocabulary: StepTypeInfo[] }>();
const emit = defineEmits<{ "update:yaml": [string]; close: []; remove: [] }>();

const step = computed<StepRecord>(() => stepAt(props.yaml, props.path) ?? {});
const type = computed(() => (typeof step.value.type === "string" ? step.value.type : ""));
const info = computed(() => props.vocabulary.find((candidate) => candidate.name === type.value));
const settings = computed<Record<string, unknown>>(() =>
  typeof step.value.settings === "object" && step.value.settings !== null ? (step.value.settings as Record<string, unknown>) : {},
);
const earlier = computed(() => earlierStepIDs(props.yaml, props.path));
const problem = computed(() =>
  info.value?.settings && step.value.settings ? settingsProblem(step.value.settings, info.value.settings, "settings") : "",
);

type Kind = "select" | "text" | "line" | "boolean" | "number" | "lines" | "yaml";
const LEAD = ["mission", "workflow", "message", "body", "plan", "then", "status", "detail", "run", "rules"];
const longText = /mission|body|detail|plan|rules/;

function kindOf(key: string, schema: SettingsSchema): Kind {
  if (schema.type === "string") return schema.enum ? "select" : longText.test(key) ? "text" : "line";
  if (schema.type === "boolean") return "boolean";
  if (schema.type === "integer" || schema.type === "number") return "number";
  if (schema.type === "array" && schema.items?.type === "string") return "lines";
  return "yaml";
}

// The setting that says what the step does leads, on its own; choices and
// switches follow in a compact block; structured settings come last.
const fields = computed(() => {
  const entries = Object.entries(info.value?.settings?.properties ?? {}).map(([key, schema]) => ({ key, schema, kind: kindOf(key, schema) }));
  const lead = entries.filter((field) => LEAD.includes(field.key) && field.kind !== "boolean").sort((a, b) => LEAD.indexOf(a.key) - LEAD.indexOf(b.key))[0];
  const rest = entries.filter((field) => field !== lead);
  return {
    lead,
    compact: rest.filter((field) => ["select", "line", "number", "boolean"].includes(field.kind)),
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
function setSetting(key: string, value: unknown): void {
  write({ ...step.value, settings: { ...settings.value, [key]: value } });
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

      <section v-if="fields.lead" class="space-y-1.5">
        <div class="flex items-center gap-1 text-xs font-medium text-muted-foreground" :title="fields.lead.schema.description">
          {{ label(fields.lead.key, fields.lead.schema) }}
          <CircleHelp v-if="fields.lead.schema.description" class="size-3 text-fg-subtle" />
        </div>
        <select
          v-if="fields.lead.kind === 'select'"
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
          <Switch
            v-if="field.kind === 'boolean'"
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
              @input="setField('retry', ($event.target as HTMLInputElement).value === '' ? undefined : { ...(step.retry as object), attempts: Number(($event.target as HTMLInputElement).value) })"
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
