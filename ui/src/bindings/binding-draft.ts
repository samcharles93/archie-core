/**
 * The bindings vocabulary: the wire shapes the daemon returns, the draft the
 * editor form edits, and the status ramp the list renders. Pure, so it reads
 * (and would test) without the page's fetch wiring -- the same split
 * binding-editor.jsx had from bindings.jsx in the Preact build.
 */

import type { EventType } from "../captures/event-types";

/**
 * A binding as GET /api/bindings returns it. Secret is never returned: the
 * daemon strips it from every read, and a blank secret on update is its
 * server-side "keep the current one".
 */
export interface Binding {
  id: string;
  name: string;
  matcher?: { source?: string };
  mapping_id?: string;
  /** Optional CEL over the mapping's parameters; an excluded event is not dispatched. */
  filter?: string;
  workflow?: string;
  /** A pin is a complete owner/repo pair or not a pin at all. */
  owner?: string;
  repo?: string;
  status?: string;
  version?: number;
}

/** The fields of GET /api/mappings this page needs. */
export interface MappingOption {
  id: string;
  name: string;
  event_type_id?: string;
}

/** The mappings that belong to one event type, which are the ones a binding
 * on that type can use. */
export function mappingsForEventType(mappings: MappingOption[], eventTypeId: string): MappingOption[] {
  if (!eventTypeId) return [];
  return mappings.filter((m) => m.event_type_id === eventTypeId);
}

/** One entry of GET /api/workflows' `definitions`. */
export interface WorkflowOption {
  id: string;
  name: string;
  enabled?: boolean;
}

/** The Badge variants a binding status maps onto. */
export type StatusKind = "ok" | "warn" | "idle";

const STATUS_LABELS: Record<string, string> = {
  pending_approval: "pending approval",
  armed: "armed",
};

const STATUS_KINDS: Record<string, StatusKind> = {
  pending_approval: "warn",
  armed: "ok",
};

/** A status the daemon grows later renders as itself in the neutral kind,
 * rather than as an unlabelled pill. */
export function statusLabel(status?: string): string {
  if (!status) return "unknown";
  return STATUS_LABELS[status] ?? status;
}

export function statusKind(status?: string): StatusKind {
  return STATUS_KINDS[status ?? ""] ?? "idle";
}

/**
 * The editor's field state.
 */
export interface BindingDraft {
  id: string | null;
  name: string;
  /** The event type the binding applies to; its source is the binding's. */
  eventTypeId: string;
  mappingId: string;
  filter: string;
  workflow: string;
  owner: string;
  repo: string;
  secret: string;
}

export function emptyDraft(): BindingDraft {
  return {
    id: null,
    name: "",
    eventTypeId: "",
    mappingId: "",
    filter: "",
    workflow: "",
    owner: "",
    repo: "",
    secret: generateSecret(),
  };
}

export function draftFromBinding(binding: Binding, mappings: MappingOption[]): BindingDraft {
  const mapping = mappings.find((m) => m.id === binding.mapping_id);
  return {
    id: binding.id,
    name: binding.name,
    eventTypeId: mapping?.event_type_id || "",
    mappingId: binding.mapping_id || "",
    filter: binding.filter || "",
    workflow: binding.workflow || "",
    owner: binding.owner || "",
    repo: binding.repo || "",
    // Deliberately blank: the secret is never sent to the browser, and an
    // empty one on update keeps whatever the sender already signs with.
    secret: "",
  };
}

/** The body POST /api/bindings and PATCH /api/bindings/{id} both accept. */
export function bindingPayload(draft: BindingDraft, eventTypes: EventType[]): Record<string, unknown> {
  const eventType = eventTypes.find((t) => t.id === draft.eventTypeId);
  return {
    name: draft.name,
    matcher: { source: eventType?.source || "" },
    mapping_id: draft.mappingId,
    filter: draft.filter.trim(),
    workflow: draft.workflow,
    owner: draft.owner,
    repo: draft.repo,
    secret: draft.secret,
  };
}

/** A 32-byte hex signing secret for a new binding. */
export function generateSecret(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(32));
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

/** The repo pin a row shows, or an em dash when the binding takes the
 * single-configured-repo default. */
export function repoPin(binding: Binding): string {
  return binding.owner && binding.repo ? `${binding.owner}/${binding.repo}` : "—";
}

/** The event type a row shows: its mapping's. */
export function bindingEventType(binding: Binding, mappings: MappingOption[]): string | undefined {
  return mappings.find((m) => m.id === binding.mapping_id)?.event_type_id;
}
