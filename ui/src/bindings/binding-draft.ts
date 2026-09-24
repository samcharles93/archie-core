/**
 * The bindings vocabulary: the wire shapes the daemon returns, the draft the
 * editor form edits, and the status ramp the list renders. Pure, so it reads
 * (and would test) without the page's fetch wiring -- the same split
 * binding-editor.jsx had from bindings.jsx in the Preact build.
 */

import type { EventType } from "../captures/event-types";

/**
 * A binding as GET /api/bindings returns it. Signing belongs to the source the
 * matcher names; unsigned is set when that source takes unsigned events.
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
  /** The mapped parameter holding "owner/name", when the repository comes from the event. */
  repo_param?: string;
  /** Each workflow input from a mapped parameter or a constant. */
  inputs?: Record<string, InputSource>;
  status?: string;
  version?: number;
  unsigned?: boolean;
}

/** One workflow input's assignment on the wire. */
export interface InputSource {
  param?: string;
  value?: unknown;
}

/** A mapped parameter: its name and field type. */
export interface MappingField {
  name: string;
  type: string;
}

/** The fields of GET /api/mappings this page needs. */
export interface MappingOption {
  id: string;
  name: string;
  event_type_id?: string;
  fields?: MappingField[];
}

/** The mappings that belong to one event type, which are the ones a binding
 * on that type can use. */
export function mappingsForEventType(
  mappings: MappingOption[],
  eventTypeId: string,
): MappingOption[] {
  if (!eventTypeId) return [];
  return mappings.filter((m) => m.event_type_id === eventTypeId);
}

/** One entry of GET /api/workflows' `definitions`. */
export interface WorkflowOption {
  id: string;
  name: string;
  enabled?: boolean;
  /** The inputs the workflow declares, by name. */
  inputs?: Record<string, { type: string; required?: boolean }>;
  /** none, optional or required; a workflow that declares none needs a repository. */
  repository?: string;
}

/** Whether a binding for this workflow may name a repository. */
export function takesRepository(workflow?: WorkflowOption): boolean {
  return (workflow?.repository ?? "required") !== "none";
}

/** A mapping's parameters a workflow input of this type can read. */
export function paramsForType(
  fields: MappingField[],
  type: string,
): MappingField[] {
  return fields.filter(
    (f) => type === "any" || f.type === "any" || f.type === type,
  );
}

/** One input's assignment as the editor holds it: a parameter name, or the
 * constant's text. */
export interface InputDraft {
  param: string;
  value: string;
}

/**
 * The constant an input's text stands for, in the input's declared type. Text
 * that does not read as that type is returned as-is, so the server's type check
 * names the mismatch rather than the editor guessing.
 */
export function constantValue(text: string, type: string): unknown {
  switch (type) {
    case "string":
      return text;
    case "number":
      return text.trim() !== "" && !Number.isNaN(Number(text))
        ? Number(text)
        : text;
    case "bool":
      return text === "true" ? true : text === "false" ? false : text;
    default:
      try {
        return JSON.parse(text);
      } catch {
        return text;
      }
  }
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
  repoParam: string;
  inputs: Record<string, InputDraft>;
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
    repoParam: "",
    inputs: {},
  };
}

export function draftFromBinding(
  binding: Binding,
  mappings: MappingOption[],
): BindingDraft {
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
    repoParam: binding.repo_param || "",
    inputs: Object.fromEntries(
      Object.entries(binding.inputs ?? {}).map(([name, src]) => [
        name,
        {
          param: src.param ?? "",
          value: src.param
            ? ""
            : typeof src.value === "string"
              ? src.value
              : JSON.stringify(src.value),
        },
      ]),
    ),
  };
}

/** The body POST /api/bindings and PATCH /api/bindings/{id} both accept. */
export function bindingPayload(
  draft: BindingDraft,
  eventTypes: EventType[],
  workflow?: WorkflowOption,
): Record<string, unknown> {
  const eventType = eventTypes.find((t) => t.id === draft.eventTypeId);
  const declared = workflow?.inputs ?? {};
  const inputs: Record<string, InputSource> = {};
  for (const [name, spec] of Object.entries(declared)) {
    const src = draft.inputs[name];
    if (src?.param) inputs[name] = { param: src.param };
    else if (src && src.value !== "")
      inputs[name] = { value: constantValue(src.value, spec.type) };
  }
  const repository = takesRepository(workflow);
  return {
    name: draft.name,
    matcher: { source: eventType?.source || "" },
    mapping_id: draft.mappingId,
    filter: draft.filter.trim(),
    workflow: draft.workflow,
    owner: repository && !draft.repoParam ? draft.owner : "",
    repo: repository && !draft.repoParam ? draft.repo : "",
    repo_param: repository ? draft.repoParam : "",
    inputs,
  };
}

/** The repo pin a row shows, or an em dash when the binding takes the
 * single-configured-repo default. */
export function repoPin(binding: Binding): string {
  if (binding.repo_param) return `from ${binding.repo_param}`;
  return binding.owner && binding.repo
    ? `${binding.owner}/${binding.repo}`
    : "—";
}

/** The event type a row shows: its mapping's. */
export function bindingEventType(
  binding: Binding,
  mappings: MappingOption[],
): string | undefined {
  return mappings.find((m) => m.id === binding.mapping_id)?.event_type_id;
}
