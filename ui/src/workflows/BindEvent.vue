<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { ArrowLeft, Braces, Type } from "@lucide/vue";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
import { statusKind, statusLabel, takesRepository, type Binding } from "@/bindings/binding-draft";
import type { Capture } from "@/captures/state";
import { sourceURL } from "@/sources/source-signing";
import type { EventType } from "@/captures/event-types";
import type { Mapping, Preview } from "@/mappings/state";
import {
  bindingInputs,
  captureBody,
  captureHeaders,
  mappingFields,
  payloadPaths,
  problems,
  REPOSITORY_FIELD,
  rowsFromBinding,
  shortValue,
  type InputRow,
  type InputSpec,
} from "./bind-event";
import { workflowField } from "./workflow-edit";

const props = defineProps<{ yaml: string; binding: Binding | null }>();
const emit = defineEmits<{ back: []; saved: [] }>();

const NEW_SOURCE = "__new__";
const workflow = computed(() => String(workflowField(props.yaml, "id") ?? ""));
const inputs = computed(() =>
  Object.entries((workflowField(props.yaml, "inputs") as Record<string, InputSpec> | undefined) ?? {}),
);
const repository = computed(() => takesRepository({ id: "", name: "", repository: String(workflowField(props.yaml, "repository") ?? "") || undefined }));

const sources = ref<string[]>([]);
const captures = ref<Capture[]>([]);
const eventTypes = ref<EventType[]>([]);
const mappings = ref<Mapping[]>([]);
const bindings = ref<Binding[]>([]);
const loading = ref(true);

const source = ref("");
const newSource = ref("");
const captureId = ref<number | null>(null);
const rows = ref<Record<string, InputRow>>({});
const repositoryPath = ref("");
const preview = ref<Preview | null>(null);
const saving = ref(false);
const error = ref("");
const status = ref(props.binding?.status ?? "");

async function loadCaptures(): Promise<void> {
  const listed = await api.captures<{ captures?: Capture[] }>(100).catch(() => null);
  captures.value = listed?.captures ?? [];
}

onMounted(async () => {
  const [listed, types, mapped, bound] = await Promise.all([
    api.sources<{ sources?: { path: string }[] }>().catch(() => null),
    api.eventTypes<{ event_types?: EventType[] }>().catch(() => null),
    api.mappings<{ mappings?: Mapping[] }>().catch(() => null),
    api.bindings<{ bindings?: Binding[] }>().catch(() => null),
    loadCaptures(),
  ]);
  sources.value = (listed?.sources ?? []).map((s) => s.path);
  eventTypes.value = types?.event_types ?? [];
  mappings.value = mapped?.mappings ?? [];
  bindings.value = bound?.bindings ?? [];
  if (props.binding) {
    source.value = props.binding.matcher?.source ?? "";
    const mapping = mappings.value.find((m) => String(m.id) === props.binding?.mapping_id);
    const filled = rowsFromBinding(props.binding, mapping?.fields ?? []);
    rows.value = filled.rows;
    repositoryPath.value = filled.repositoryPath;
  } else if (sources.value.length === 1) {
    source.value = sources.value[0]!;
  }
  for (const [name] of inputs.value) rows.value[name] ??= { from: "path", path: "", value: "" };
  loading.value = false;
});
useLiveResource("captures", () => void loadCaptures(), 500);

const sourcePath = computed(() => (source.value === NEW_SOURCE ? newSource.value.trim() : source.value));
const hookURL = computed(() => sourceURL(window.location.origin, sourcePath.value));
const sourceCaptures = computed(() => captures.value.filter((c) => c.source === sourcePath.value));
// The newest event of the source is the example until another is picked.
watch(sourceCaptures, (list) => {
  if (!list.some((c) => c.id === captureId.value)) captureId.value = list[0]?.id ?? null;
}, { immediate: true });
const capture = computed(() => sourceCaptures.value.find((c) => c.id === captureId.value));
const payload = computed(() => captureBody(capture.value));
const paths = computed(() => payloadPaths(payload.value));

const fields = computed(() => mappingFields(inputs.value, rows.value, repository.value ? repositoryPath.value : ""));
let previewTimer: ReturnType<typeof setTimeout> | undefined;
watch([fields, captureId], () => {
  clearTimeout(previewTimer);
  if (captureId.value === null || !fields.value.length) {
    preview.value = null;
    return;
  }
  const id = captureId.value;
  previewTimer = setTimeout(async () => {
    const result = await api.mappingPreview<Preview>(id, fields.value).catch(() => null);
    if (id === captureId.value) preview.value = result;
  }, 250);
}, { deep: true, immediate: true });

const blockers = computed(() => {
  if (!sourcePath.value) return ["pick a source"];
  if (!capture.value) return [`no event from ${sourcePath.value} yet to preview against`];
  if (payload.value === null) return ["the picked event's body is not JSON"];
  return problems(inputs.value, rows.value, preview.value);
});

function resolved(name: string): { value?: string; failure?: string } {
  const failure = preview.value?.failures?.find((f) => f.field_name === name);
  if (failure) return { failure: failure.reason };
  const values = preview.value?.values ?? {};
  return name in values ? { value: shortValue(values[name]) } : {};
}

function when(at?: string): string {
  return at ? new Date(at).toLocaleString(undefined, { dateStyle: "short", timeStyle: "short" }) : "";
}

// A mapping another binding also reads is left as it is: the edit gets its own.
function sharedMapping(id?: string): boolean {
  return bindings.value.some((b) => b.mapping_id === id && b.id !== props.binding?.id);
}

// An edit keeps its mapping's event type; a capture that arrived unidentified
// becomes the example of a new one.
async function eventTypeFor(c: Capture): Promise<string> {
  const kept = mappings.value.find((m) => String(m.id) === props.binding?.mapping_id)?.event_type_id;
  if (kept) return kept;
  if (c.event_type) return c.event_type;
  const created = await api.eventTypeCreate<EventType>({
    source: sourcePath.value,
    name: `${sourcePath.value} event`,
    example: { headers: captureHeaders(c), body: c.body ?? "" },
  });
  return created.id;
}

async function save(): Promise<void> {
  if (blockers.value.length || !capture.value) return;
  saving.value = true;
  error.value = "";
  try {
    if (source.value === NEW_SOURCE && !sources.value.includes(sourcePath.value)) await api.sourceCreate(sourcePath.value);
    const mappingBody = {
      name: `${workflow.value} ← ${sourcePath.value}`,
      event_type_id: await eventTypeFor(capture.value),
      fields: fields.value,
    };
    const existing = props.binding?.mapping_id;
    const mapping = existing && !sharedMapping(existing)
      ? await api.mappingUpdate<Mapping>(existing, mappingBody)
      : await api.mappingCreate<Mapping>(mappingBody);
    const body = {
      name: props.binding?.name || `${sourcePath.value} → ${workflow.value}`,
      matcher: { source: sourcePath.value },
      mapping_id: String(mapping.id),
      filter: props.binding?.filter ?? "",
      workflow: workflow.value,
      repo_param: repository.value && repositoryPath.value.trim() ? REPOSITORY_FIELD : "",
      owner: props.binding?.owner ?? "",
      repo: props.binding?.repo ?? "",
      inputs: bindingInputs(inputs.value, rows.value),
    };
    if (props.binding) await api.bindingUpdate(props.binding.id, body);
    else await api.bindingCreate(body);
    emit("saved");
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

async function approve(): Promise<void> {
  if (!props.binding) return;
  error.value = "";
  try {
    await api.bindingApprove(props.binding.id);
    status.value = "armed";
    emit("saved");
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}
</script>

<template>
  <div class="flex h-full flex-col">
    <header class="flex items-center gap-2 border-b border-border px-4 py-3">
      <Button type="button" variant="ghost" size="icon-sm" aria-label="Back to settings" @click="emit('back')"><ArrowLeft /></Button>
      <div class="min-w-0 flex-1 truncate text-sm font-medium">{{ binding ? binding.name : "Bind an event" }}</div>
      <Badge v-if="status" :variant="statusKind(status)">{{ statusLabel(status) }}</Badge>
    </header>

    <div v-if="loading" class="flex flex-1 items-center justify-center"><Spinner /></div>
    <form v-else class="flex-1 space-y-5 overflow-y-auto px-4 py-4" @submit.prevent="save">
      <section class="space-y-1.5">
        <label for="bind-source" class="text-xs font-medium text-muted-foreground">Source</label>
        <select id="bind-source" v-model="source" class="h-8 w-full rounded-md border border-input bg-background px-2 text-[13px]" :disabled="!!binding">
          <option value="" disabled>Pick a source</option>
          <option v-for="path in sources" :key="path" :value="path">{{ path }}</option>
          <option :value="NEW_SOURCE">New source…</option>
        </select>
        <input
          v-if="source === NEW_SOURCE"
          v-model="newSource"
          aria-label="New source path"
          placeholder="path, e.g. alertmanager"
          class="h-8 w-full rounded-md border border-input bg-background px-2 font-mono text-[13px]"
        />
      </section>

      <section v-if="sourcePath" class="space-y-1.5">
        <label for="bind-capture" class="text-xs font-medium text-muted-foreground">Example event</label>
        <select v-if="sourceCaptures.length" id="bind-capture" v-model="captureId" class="h-8 w-full rounded-md border border-input bg-background px-2 text-[13px]">
          <option v-for="c in sourceCaptures" :key="c.id" :value="c.id">#{{ c.id }} · {{ when(c.received_at) }}{{ c.event_type ? "" : " · unidentified" }}</option>
        </select>
        <p v-else class="text-xs text-fg-subtle" :title="`Send an event to ${hookURL} and it appears here`">Waiting for the first event</p>
      </section>

      <section class="space-y-1.5">
        <div class="text-xs font-medium text-muted-foreground">Inputs</div>
        <datalist id="bind-paths"><option v-for="path in paths" :key="path" :value="path" /></datalist>
        <div class="divide-y divide-border rounded-md border border-border">
          <div v-for="[name, spec] in inputs" :key="name" class="space-y-1 px-3 py-2">
            <div class="flex items-center gap-1.5 text-xs">
              <span class="font-mono">{{ name }}</span>
              <span class="text-fg-subtle">{{ spec.type ?? "string" }}</span>
              <span v-if="spec.required" class="text-danger" title="Required">*</span>
              <button
                type="button"
                class="ml-auto text-muted-foreground hover:text-foreground"
                :title="rows[name]!.from === 'path' ? 'Use a fixed value' : 'Read from the event'"
                :aria-label="`${name}: ${rows[name]!.from === 'path' ? 'use a fixed value' : 'read from the event'}`"
                @click="rows[name]!.from = rows[name]!.from === 'path' ? 'value' : 'path'"
              >
                <Type v-if="rows[name]!.from === 'path'" class="size-3.5" /><Braces v-else class="size-3.5" />
              </button>
            </div>
            <input
              v-if="rows[name]!.from === 'path'"
              v-model="rows[name]!.path"
              list="bind-paths"
              :aria-label="`${name} payload path`"
              placeholder="payload path"
              class="h-7 w-full rounded-md border border-input bg-background px-2 font-mono text-xs"
            />
            <input
              v-else
              v-model="rows[name]!.value"
              :aria-label="`${name} fixed value`"
              placeholder="fixed value"
              class="h-7 w-full rounded-md border border-input bg-background px-2 font-mono text-xs"
            />
            <p v-if="rows[name]!.from === 'path' && resolved(name).failure" class="text-xs text-danger">{{ resolved(name).failure }}</p>
            <p v-else-if="rows[name]!.from === 'path' && resolved(name).value !== undefined" class="truncate font-mono text-xs text-success" :title="resolved(name).value">{{ resolved(name).value }}</p>
          </div>
          <div v-if="repository" class="space-y-1 px-3 py-2">
            <div class="text-xs" title="Optional: owner/name of a configured repository; blank uses the configured one">Repository</div>
            <input
              v-model="repositoryPath"
              list="bind-paths"
              aria-label="Repository payload path"
              placeholder="payload path"
              class="h-7 w-full rounded-md border border-input bg-background px-2 font-mono text-xs"
            />
            <p v-if="resolved(REPOSITORY_FIELD).failure" class="text-xs text-danger">{{ resolved(REPOSITORY_FIELD).failure }}</p>
            <p v-else-if="resolved(REPOSITORY_FIELD).value !== undefined" class="truncate font-mono text-xs text-success">{{ resolved(REPOSITORY_FIELD).value }}</p>
          </div>
          <p v-if="!inputs.length && !repository" class="px-3 py-2 text-xs text-fg-subtle">The workflow declares no inputs.</p>
        </div>
      </section>

      <div class="space-y-2">
        <p v-if="error" role="alert" class="text-xs text-danger">{{ error }}</p>
        <p v-else-if="blockers.length" class="text-xs text-fg-subtle">Cannot save: {{ blockers[0] }}</p>
        <div class="flex gap-2">
          <Button type="submit" size="sm" :disabled="saving || !!blockers.length"><Spinner v-if="saving" />Save binding</Button>
          <Button v-if="binding && status === 'pending_approval'" type="button" size="sm" variant="outline" @click="approve">Approve</Button>
        </div>
      </div>
    </form>
  </div>
</template>
