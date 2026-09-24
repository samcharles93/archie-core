/**
 * The bindings vocabulary: the wire shapes the daemon returns, the draft the
 * editor form edits, and the status ramp the list renders. Pure, so it reads
 * (and would test) without the page's fetch wiring -- the same split
 * binding-editor.jsx had from bindings.jsx in the Preact build.
 */

/**
 * A binding as GET /api/bindings returns it. Signing belongs to the source the
 * matcher names; unsigned is set when that source takes unsigned events.
 */
export interface Binding {
  id: string;
  name: string;
  matcher?: { source?: string };
  mapping_id?: string;
  workflow?: string;
  /** A pin is a complete owner/repo pair or not a pin at all. */
  owner?: string;
  repo?: string;
  status?: string;
  version?: number;
  unsigned?: boolean;
}

/** The fields of GET /api/mappings this page needs. */
export interface MappingOption {
  id: string;
  name: string;
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
  source: string;
  mappingId: string;
  workflow: string;
  owner: string;
  repo: string;
}

export function emptyDraft(): BindingDraft {
  return {
    id: null,
    name: "",
    source: "",
    mappingId: "",
    workflow: "",
    owner: "",
    repo: "",
  };
}

export function draftFromBinding(binding: Binding): BindingDraft {
  return {
    id: binding.id,
    name: binding.name,
    source: binding.matcher?.source || "",
    mappingId: binding.mapping_id || "",
    workflow: binding.workflow || "",
    owner: binding.owner || "",
    repo: binding.repo || "",
  };
}

/** The body POST /api/bindings and PATCH /api/bindings/{id} both accept. */
export function bindingPayload(draft: BindingDraft): Record<string, unknown> {
  return {
    name: draft.name,
    matcher: { source: draft.source },
    mapping_id: draft.mappingId,
    workflow: draft.workflow,
    owner: draft.owner,
    repo: draft.repo,
  };
}

/** The repo pin a row shows, or an em dash when the binding takes the
 * single-configured-repo default. */
export function repoPin(binding: Binding): string {
  return binding.owner && binding.repo ? `${binding.owner}/${binding.repo}` : "—";
}

/** The matcher source a row shows: the path segment senders POST to. */
export function matcherSource(binding: Binding): string {
  return binding.matcher?.source || "—";
}
