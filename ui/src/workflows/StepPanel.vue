<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ArrowDown, ArrowUp, Link2, Plus, Trash2, X } from "@lucide/vue";
import { parse, stringify } from "yaml";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { stepTitle } from "./workflow-graph";
import {
  deleteStep,
  earlierStepIDs,
  insertStep,
  moveStep,
  replaceStep,
  stepAt,
  type StepPath,
  type StepRecord,
} from "./workflow-edit";
import { settingsProblem, type SettingsSchema, type StepTypeInfo } from "./workflow-yaml";

const props = defineProps<{ yaml: string; path: StepPath | null; vocabulary: StepTypeInfo[] }>();
const emit = defineEmits<{ "update:yaml": [string]; select: [StepPath | null] }>();

const step = computed(() => (props.path ? stepAt(props.yaml, props.path) : undefined));
const type = computed(() => (typeof step.value?.type === "string" ? step.value.type : ""));
const info = computed(() => props.vocabulary.find((candidate) => candidate.name === type.value));
const settings = computed<Record<string, unknown>>(() =>
  typeof step.value?.settings === "object" && step.value.settings !== null
    ? (step.value.settings as Record<string, unknown>)
    : {},
);
// The schema's keys arrive sorted; the setting that says what a step does
// comes first.
const LEAD = ["mission", "workflow", "message", "body", "plan", "then", "status", "detail", "run", "rules"];
const fields = computed(() =>
  Object.entries(info.value?.settings?.properties ?? {}).sort(([a], [b]) => {
    const rank = (key: string) => (LEAD.includes(key) ? LEAD.indexOf(key) : LEAD.length);
    return rank(a) - rank(b);
  }),
);
const earlier = computed(() => (props.path ? earlierStepIDs(props.yaml, props.path) : []));
const inBranch = computed(() => (props.path?.length ?? 0) > 2);
const problem = computed(() =>
  info.value?.settings && step.value?.settings ? settingsProblem(step.value.settings, info.value.settings, "settings") : "",
);

// The palette is open while nothing is selected; selecting a step, including
// the one just added, folds it away so that step's form is what shows.
const paletteOpen = ref(true);
watch(() => JSON.stringify(props.path), () => (paletteOpen.value = !props.path), { immediate: true });

const groups = computed(() => {
  const byGroup = new Map<string, StepTypeInfo[]>();
  for (const candidate of props.vocabulary) {
    const group = candidate.name.split(".")[0];
    byGroup.set(group, [...(byGroup.get(group) ?? []), candidate]);
  }
  return [...byGroup.entries()];
});

function write(next: StepRecord): void {
  // id and type lead, the way a step is read.
  const { id, type: stepType, ...rest } = next;
  if (props.path) emit("update:yaml", replaceStep(props.yaml, props.path, { id, type: stepType, ...rest }));
}
function setField(key: string, value: unknown): void {
  write({ ...step.value, [key]: value });
}
function setSetting(key: string, value: unknown): void {
  write({ ...step.value, settings: { ...settings.value, [key]: value } });
}

function add(stepType: string): void {
  const after = props.path && props.path.length === 2 ? Number(props.path[1]) : Number.MAX_SAFE_INTEGER;
  const inserted = insertStep(props.yaml, after, stepType);
  emit("update:yaml", inserted.source);
  emit("select", inserted.path);
}
function move(delta: -1 | 1): void {
  if (!props.path) return;
  const moved = moveStep(props.yaml, props.path, delta);
  emit("update:yaml", moved.source);
  emit("select", moved.path);
}
function remove(): void {
  if (!props.path) return;
  emit("update:yaml", deleteStep(props.yaml, props.path));
  emit("select", null);
}

// A reference is inserted into the text field last focused, which is how a
// later step is connected to an earlier step's result.
const focused = ref<string | null>(null);
function reference(id: string, field: "summary" | "result"): void {
  const key = focused.value ?? fields.value.find(([, schema]) => schema.type === "string")?.[0];
  if (!key) return;
  const current = typeof settings.value[key] === "string" ? (settings.value[key] as string) : "";
  const token = field === "summary" ? `{{ steps.${id}.summary }}` : `{{ steps.${id}.result }}`;
  setSetting(key, current ? `${current.trimEnd()} ${token}` : token);
}

const longText = /mission|body|detail|plan|rules/;
function kindOf(key: string, schema: SettingsSchema): "select" | "text" | "line" | "boolean" | "number" | "lines" | "yaml" {
  if (schema.type === "string") return schema.enum ? "select" : longText.test(key) ? "text" : "line";
  if (schema.type === "boolean") return "boolean";
  if (schema.type === "integer" || schema.type === "number") return "number";
  if (schema.type === "array" && schema.items?.type === "string") return "lines";
  return "yaml";
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
function asYaml(value: unknown): string {
  return value === undefined ? "" : stringify(value).trimEnd();
}
function label(key: string, schema: SettingsSchema): string {
  return schema.title ?? key.replaceAll("_", " ").replace(/^./, (c) => c.toUpperCase());
}
</script>

<template>
  <aside class="flex h-[560px] flex-col gap-3 overflow-y-auto rounded-lg border border-border bg-card p-3 text-sm">
    <template v-if="step && path">
      <div class="flex items-start gap-2">
        <div class="min-w-0">
          <div class="truncate font-medium">{{ stepTitle(type) }}</div>
          <div class="font-mono text-[11px] text-fg-subtle">{{ type }}</div>
        </div>
        <Button type="button" variant="ghost" size="icon" class="ml-auto size-7" aria-label="Close" @click="emit('select', null)"><X /></Button>
      </div>
      <p v-if="problem" class="rounded bg-danger/10 px-2 py-1 text-xs text-danger" role="alert">{{ problem }}</p>

      <label class="flex flex-col gap-1">
        <span class="text-xs text-muted-foreground">Step id</span>
        <Input
          :model-value="(step.id as string) ?? ''"
          class="h-8 font-mono"
          placeholder="name it so later steps can use its result"
          @update:model-value="setField('id', String($event))"
        />
      </label>

      <template v-for="[key, schema] in fields" :key="key">
        <label v-if="kindOf(key, schema) === 'boolean'" class="flex items-start gap-2" :title="schema.description">
          <input type="checkbox" class="mt-0.5" :checked="settings[key] === true" @change="setSetting(key, ($event.target as HTMLInputElement).checked || undefined)" />
          <span>{{ label(key, schema) }}<span v-if="schema.description" class="block text-xs text-fg-subtle">{{ schema.description }}</span></span>
        </label>
        <label v-else class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">{{ label(key, schema) }}</span>
          <select
            v-if="kindOf(key, schema) === 'select'"
            class="h-8 rounded-md border border-input bg-background px-2"
            :value="(settings[key] as string) ?? ''"
            @change="setSetting(key, ($event.target as HTMLSelectElement).value || undefined)"
          >
            <option value="">(default)</option>
            <option v-for="option in schema.enum" :key="option" :value="option">{{ option }}</option>
          </select>
          <Textarea
            v-else-if="kindOf(key, schema) === 'text'"
            :model-value="(settings[key] as string) ?? ''"
            class="min-h-28 font-mono text-xs"
            @focus="focused = key"
            @update:model-value="setSetting(key, String($event))"
          />
          <Input
            v-else-if="kindOf(key, schema) === 'line'"
            :model-value="(settings[key] as string) ?? ''"
            class="h-8"
            @focus="focused = key"
            @update:model-value="setSetting(key, String($event))"
          />
          <Input
            v-else-if="kindOf(key, schema) === 'number'"
            type="number"
            :model-value="(settings[key] as number) ?? ''"
            class="h-8"
            @update:model-value="setSetting(key, $event === '' ? undefined : Number($event))"
          />
          <Textarea
            v-else-if="kindOf(key, schema) === 'lines'"
            :model-value="Array.isArray(settings[key]) ? (settings[key] as string[]).join('\n') : ''"
            class="min-h-16 font-mono text-xs"
            placeholder="one per line"
            @update:model-value="setSetting(key, String($event).split('\n').filter(Boolean))"
          />
          <template v-else>
            <Textarea
              :model-value="asYaml(settings[key])"
              class="min-h-24 font-mono text-xs"
              placeholder="YAML"
              @change="setYamlSetting(key, ($event.target as HTMLTextAreaElement).value)"
            />
            <span v-if="yamlErrors[key]" class="text-xs text-danger">{{ yamlErrors[key] }}</span>
          </template>
          <span v-if="schema.description" class="text-xs text-fg-subtle">{{ schema.description }}</span>
        </label>
      </template>

      <div v-if="earlier.length && fields.some(([, schema]) => schema.type === 'string')" class="space-y-1">
        <span class="flex items-center gap-1 text-xs text-muted-foreground"><Link2 class="size-3" aria-hidden="true" />Use an earlier step's result</span>
        <div class="flex flex-wrap gap-1">
          <template v-for="id in earlier" :key="id">
            <button type="button" class="rounded bg-secondary px-1.5 py-0.5 font-mono text-[11px] hover:bg-secondary/70" @click="reference(id, 'summary')">{{ id }}.summary</button>
            <button type="button" class="rounded bg-secondary px-1.5 py-0.5 font-mono text-[11px] hover:bg-secondary/70" @click="reference(id, 'result')">{{ id }}.result</button>
          </template>
        </div>
      </div>

      <details class="space-y-2">
        <summary class="cursor-pointer text-xs text-muted-foreground">When, failure and retry</summary>
        <label class="mt-2 flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">Run only when</span>
          <Input :model-value="(step.when as string) ?? ''" class="h-8 font-mono" placeholder="steps.assess.result.fit" @update:model-value="setField('when', String($event))" />
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">If it fails</span>
          <select class="h-8 rounded-md border border-input bg-background px-2" :value="(step.on_failure as string) ?? ''" @change="setField('on_failure', ($event.target as HTMLSelectElement).value || undefined)">
            <option value="">Park the task</option>
            <option value="continue">Continue to the next step</option>
          </select>
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">Attempts</span>
          <Input
            type="number"
            min="1"
            max="10"
            :model-value="((step.retry as { attempts?: number } | undefined)?.attempts) ?? ''"
            class="h-8"
            @update:model-value="setField('retry', $event === '' ? undefined : { ...(step.retry as object), attempts: Number($event) })"
          />
        </label>
      </details>

      <div class="mt-auto flex flex-wrap gap-1.5 border-t border-border pt-3">
        <Button type="button" variant="outline" size="sm" @click="move(-1)"><ArrowUp /> Up</Button>
        <Button type="button" variant="outline" size="sm" @click="move(1)"><ArrowDown /> Down</Button>
        <Button type="button" variant="ghost" size="sm" class="ml-auto text-danger" @click="remove"><Trash2 /> Remove</Button>
      </div>
    </template>
    <div v-else class="text-xs text-fg-subtle">Select a step to edit it.</div>

    <details
      v-if="!inBranch"
      :open="paletteOpen"
      class="space-y-2 border-t border-border pt-3"
      @toggle="paletteOpen = ($event.target as HTMLDetailsElement).open"
    >
      <summary class="cursor-pointer text-xs font-medium">{{ step ? "Add a step after this one" : "Add a step" }}</summary>
      <div v-for="[group, members] in groups" :key="group" class="space-y-1">
        <div class="text-[11px] tracking-wide text-fg-subtle uppercase">{{ group }}</div>
        <div class="flex flex-wrap gap-1">
          <button
            v-for="candidate in members"
            :key="candidate.name"
            type="button"
            class="flex items-center gap-1 rounded border border-border px-1.5 py-0.5 text-xs hover:bg-secondary"
            :title="candidate.name"
            @click="add(candidate.name)"
          >
            <Plus class="size-3" aria-hidden="true" />{{ stepTitle(candidate.name) }}
          </button>
        </div>
      </div>
    </details>
  </aside>
</template>
