import { ref } from "vue";

import { api, classifyActionError, type ActionErrorKind } from "@/lib/api";
import type { EventType } from "@/captures/event-types";
import { uniqueFieldName, type FieldPick, type FieldType } from "./mapping-fields";

/**
 * The mappings page's shared state: the saved mappings, the captured events a
 * mapping can be previewed against, and the one editing session in flight.
 *
 * The list, the editor overlay, the bound-fields table and the payload tree all
 * read and mutate it, so it lives in a module rather than being threaded down
 * through a chain of props.
 */

/** One bound field, as the daemon stores it (mapping.Field). */
export interface MappingField {
  name: string;
  path: string;
  type: FieldType;
  required: boolean;
}

/**
 * A named set of bound fields belonging to one event type (mapping.Mapping).
 * The daemon checks the fields against the type's schema on save, and counts
 * each event the mapping resolves.
 */
export interface Mapping {
  id: number;
  name: string;
  source_hint?: string;
  event_type_id?: string;
  fields?: MappingField[];
  match_count?: number;
  last_matched_at?: string;
}

/** A captured inbound event. Its body is redacted before it is stored, and is
 * the only thing a path is ever resolved against. */
export interface Capture {
  id: number;
  source?: string;
  received_at?: string;
  body: string;
}

/** One field the preview could not resolve, and why. */
export interface PreviewFailure {
  field_name: string;
  path?: string;
  reason: string;
}

/** What POST /api/mappings/preview answers: the fields that resolved, by name,
 * and every field that did not. */
export interface Preview {
  values?: Record<string, unknown>;
  failures?: PreviewFailure[];
}

/** One editing session: the working copy of a mapping, the example payload it
 * is bound from, and the last preview of the two together. */
export interface MappingDraft {
  /** null until the first save. */
  id: number | null;
  name: string;
  sourceHint: string;
  /** The event type the mapping belongs to; its schema checks the fields. */
  eventTypeId: string;
  fields: MappingField[];
  /** The capture the payload and the preview resolve against, or null. */
  captureId: number | null;
  /** The parsed capture body, or null when no capture is picked. */
  payload: unknown;
  payloadError: string | null;
  preview: Preview | null;
}

/** An action failure: the daemon's three kinds, plus a local one for an action
 * the editor cannot attempt yet. */
export type MappingErrorKind = ActionErrorKind | "incomplete";

export interface ActionFailure {
  kind: MappingErrorKind;
  message: string;
}

export const mappings = ref<Mapping[]>([]);
export const eventTypes = ref<EventType[]>([]);
export const captures = ref<Capture[]>([]);
/** Whether this deployment has capture storage at all. GET /api/captures
 * answers {"enabled": false} rather than failing, which is what lets an empty
 * picker say whether nothing has been captured yet or nothing can be. */
export const capturesEnabled = ref(true);
/** true until the first list load settles. */
export const listLoading = ref(true);
/** A failed list load. Distinct from an action failure: the list is unusable,
 * where a refused action leaves it perfectly readable. */
export const loadError = ref<string | null>(null);

/** Whether the editor overlay is showing the draft. */
export const editorOpen = ref(false);
/** The editing session. Always present, so the form's bindings need no null
 * branch; `editorOpen` says whether it is on screen. */
export const draft = ref<MappingDraft>(blankDraft());
export const saving = ref(false);

/** The last failed operator action, classified for rendering. One slot, as
 * Preact had: preview and save report into the same place. */
export const actionError = ref<ActionFailure | null>(null);

/** The mapping awaiting delete confirmation. */
export const pendingDelete = ref<Mapping | null>(null);

function blankDraft(): MappingDraft {
  return {
    id: null,
    name: "",
    sourceHint: "",
    eventTypeId: "",
    fields: [],
    captureId: null,
    payload: null,
    payloadError: null,
    preview: null,
  };
}

export async function loadMappings(): Promise<void> {
  listLoading.value = true;
  try {
    // Capture storage is optional infrastructure -- a deployment can serve
    // mappings without it -- so a failing capture leg degrades to an empty
    // picker instead of taking the mappings list down with it.
    const [listed, captured, types] = await Promise.all([
      api.mappings<{ mappings?: Mapping[] }>(),
      api.captures<{ captures?: Capture[]; enabled?: boolean }>(100).catch(() => null),
      api.eventTypes<{ event_types?: EventType[] }>().catch(() => null),
    ]);
    mappings.value = listed.mappings || [];
    eventTypes.value = types?.event_types || [];
    captures.value = captured?.captures || [];
    capturesEnabled.value = captured?.enabled !== false;
    loadError.value = null;
  } catch (err) {
    loadError.value = String((err as Error).message || err);
  } finally {
    listLoading.value = false;
  }
}

export function startCreate(): void {
  draft.value = blankDraft();
  actionError.value = null;
  editorOpen.value = true;
}

export function startEdit(mapping: Mapping): void {
  draft.value = {
    ...blankDraft(),
    id: mapping.id,
    name: mapping.name,
    sourceHint: mapping.source_hint || "",
    eventTypeId: mapping.event_type_id || "",
    // Copied, not aliased: the editor must not write through to the loaded list.
    fields: (mapping.fields || []).map((field) => ({ ...field })),
  };
  actionError.value = null;
  editorOpen.value = true;
}

export function closeEditor(): void {
  editorOpen.value = false;
  actionError.value = null;
}

/**
 * Picks the example event the payload panel and the preview resolve against.
 * The body is parsed once, here, so a body that is not JSON reads as exactly
 * that rather than as a payload whose paths all failed.
 */
export function selectCapture(value: string): void {
  const capture = captures.value.find((candidate) => String(candidate.id) === value) ?? null;
  const current = draft.value;
  current.captureId = capture ? capture.id : null;
  current.preview = null;
  if (!capture) {
    current.payload = null;
    current.payloadError = null;
    return;
  }
  try {
    current.payload = JSON.parse(capture.body);
    current.payloadError = null;
  } catch {
    current.payload = null;
    current.payloadError = "This capture's body is not valid JSON.";
  }
}

/** Binds one clicked payload value as a new required field. */
export function addField(pick: FieldPick): void {
  const current = draft.value;
  const name = uniqueFieldName(
    current.fields.map((field) => field.name),
    pick.name,
  );
  current.fields.push({ name, path: pick.path, type: pick.type, required: true });
  // Any edit invalidates the preview: it described the fields as they were.
  current.preview = null;
}

export function updateField(index: number, patch: Partial<MappingField>): void {
  const current = draft.value;
  current.fields[index] = { ...current.fields[index], ...patch };
  current.preview = null;
}

export function removeField(index: number): void {
  draft.value.fields.splice(index, 1);
  draft.value.preview = null;
}

export async function runPreview(): Promise<void> {
  const current = draft.value;
  if (current.captureId === null) {
    actionError.value = { kind: "incomplete", message: "Pick a captured event to preview against." };
    return;
  }
  try {
    current.preview = await api.mappingPreview<Preview>(current.captureId, current.fields);
    actionError.value = null;
  } catch (err) {
    actionError.value = classifyActionError(err);
  }
}

export async function saveMapping(): Promise<void> {
  if (saving.value) return;
  const current = draft.value;
  const body = {
    name: current.name,
    source_hint: current.sourceHint,
    event_type_id: current.eventTypeId,
    fields: current.fields,
  };
  saving.value = true;
  try {
    if (current.id !== null) await api.mappingUpdate(String(current.id), body);
    else await api.mappingCreate(body);
    closeEditor();
    await loadMappings();
  } catch (err) {
    actionError.value = classifyActionError(err);
  } finally {
    saving.value = false;
  }
}

export function requestDelete(mapping: Mapping): void {
  pendingDelete.value = mapping;
}

export function cancelDelete(): void {
  pendingDelete.value = null;
}

/**
 * Confirms the pending delete. A failure lands in the action alert and leaves
 * the list as it was: a refused delete says nothing about whether the daemon is
 * reachable, and reporting it as "cannot reach archied" would be untrue.
 */
export async function confirmDelete(): Promise<void> {
  const target = pendingDelete.value;
  if (!target) return;
  pendingDelete.value = null;
  try {
    await api.mappingDelete(String(target.id));
    await loadMappings();
  } catch (err) {
    actionError.value = classifyActionError(err);
  }
}

/** How a failed action reads as an Alert title. The message under it is the
 * server's own, which is the part an operator can act on. */
export function actionErrorTitle(kind: MappingErrorKind): string {
  switch (kind) {
    case "session-expired":
      return "Your session expired";
    case "refused":
      return "archied refused that";
    case "incomplete":
      return "Not ready to preview";
    default:
      return "archied could not complete that";
  }
}
