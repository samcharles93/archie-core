import { ref } from "vue";

import { api } from "@/lib/api";
import { captures } from "./state";
import {
  cleanRule,
  parseHeaderLines,
  type EventType,
  type Proposal,
  type Rule,
} from "./event-types";

/**
 * The inspector's event types: the named ones, and the groups of unidentified
 * captures proposed as types. The server groups and infers; this module holds
 * the read and one editing session at a time.
 */

interface EventTypesResponse {
  enabled?: boolean;
  event_types?: EventType[];
  proposals?: Proposal[];
}

export const eventTypes = ref<EventType[]>([]);
export const proposals = ref<Proposal[]>([]);
export const eventTypesEnabled = ref(true);
export const eventTypesError = ref<string | null>(null);

/** The editor's modes: naming a proposal, pasting an example, editing a type. */
export type DraftMode = "proposal" | "paste" | "edit";

export interface Draft {
  mode: DraftMode;
  id: string | null;
  source: string;
  name: string;
  rule: Rule;
  headersText: string;
  body: string;
  exampleHeaders: Record<string, string>;
}

export const draft = ref<Draft | null>(null);
export const saving = ref(false);
export const saveError = ref<string | null>(null);

export async function loadEventTypes(): Promise<void> {
  try {
    const res = await api.eventTypes<EventTypesResponse>();
    eventTypesEnabled.value = res.enabled !== false;
    eventTypes.value = res.event_types || [];
    proposals.value = res.proposals || [];
    eventTypesError.value = null;
  } catch (err) {
    eventTypesError.value = (err as Error).message || String(err);
  }
}

function copyRule(rule: Rule): Rule {
  return {
    headers: (rule.headers || []).map((h) => ({ ...h })),
    payload: (rule.payload || []).map((p) => ({ ...p })),
  };
}

function open(next: Draft): void {
  saveError.value = null;
  draft.value = next;
}

export function nameProposal(p: Proposal): void {
  const sample = captures.value.find((c) => String(c.id) === p.sample_id);
  open({
    mode: "proposal",
    id: null,
    source: p.source,
    name: "",
    rule: copyRule(p.rule),
    headersText: "",
    body: sample?.body || "",
    exampleHeaders: p.headers || {},
  });
}

export function pasteExample(source = ""): void {
  open({
    mode: "paste",
    id: null,
    source,
    name: "",
    rule: { headers: [], payload: [] },
    headersText: "",
    body: "",
    exampleHeaders: {},
  });
}

export function editEventType(t: EventType): void {
  open({
    mode: "edit",
    id: t.id,
    source: t.source,
    name: t.name,
    rule: copyRule(t.rule),
    headersText: "",
    body: "",
    exampleHeaders: {},
  });
}

export function closeDraft(): void {
  draft.value = null;
}

export async function saveDraft(): Promise<void> {
  const d = draft.value;
  if (!d) return;
  saving.value = true;
  try {
    if (d.mode === "edit" && d.id) {
      await api.eventTypeUpdate(d.id, {
        name: d.name,
        rule: cleanRule(d.rule),
      });
    } else {
      const headers =
        d.mode === "paste" ? parseHeaderLines(d.headersText) : d.exampleHeaders;
      await api.eventTypeCreate({
        source: d.source,
        name: d.name,
        // A pasted example's rule is inferred by the server from what was pasted.
        rule: d.mode === "paste" ? undefined : cleanRule(d.rule),
        example: { headers, body: d.body },
      });
    }
    draft.value = null;
    await loadEventTypes();
  } catch (err) {
    saveError.value = (err as Error).message || String(err);
  } finally {
    saving.value = false;
  }
}

/** The type awaiting a delete confirmation: its events become unidentified. */
export const pendingDelete = ref<EventType | null>(null);

export async function confirmDelete(): Promise<void> {
  const t = pendingDelete.value;
  pendingDelete.value = null;
  if (!t) return;
  try {
    await api.eventTypeDelete(t.id);
    await loadEventTypes();
  } catch (err) {
    eventTypesError.value = (err as Error).message || String(err);
  }
}
