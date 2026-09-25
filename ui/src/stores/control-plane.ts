import { defineStore } from "pinia";
import { computed, onScopeDispose, reactive, ref, watch } from "vue";
import { useLiveUpdatesStore } from "./live-updates.ts";

import { diffValues, saveInOrder, type Change } from "../settings/changes.ts";

export interface ResourceDescriptor {
  kind: string;
  title: string;
  schema_json: string;
  commands: string[];
  apply_mode?: "live" | "restart-required" | "domain-managed";
  query_service?: string;
  command_service?: string;
  /** Factory value this resource can be restored to, as JSON. */
  defaults_json?: string;
}

export interface ControlPlaneResource {
  kind: string;
  version: number;
  value: unknown;
  updated_at?: string;
}

interface ResourceResponse {
  resource: ControlPlaneResource;
}

/** One process's report about one resource kind, as the server derives it.
 * `state` is liveness only: the server knows whether a record went stale, not
 * which version the page is showing. */
export interface ApplyStatusRecord {
  process: string;
  kind: string;
  applied_version: number;
  error?: string;
  reported_at: string;
  state: "current" | "failed" | "unknown";
}

export interface ApplyStatusResponse {
  records: ApplyStatusRecord[];
  processes: string[];
}

/** What the settings page says about one process for one resource. */
export type ApplyState =
  "running" | "pending-restart" | "failed" | "unknown" | "not-reporting";

export interface ApplyStatusRow {
  process: string;
  state: ApplyState;
  version: number;
  error: string;
}

/** applyStatusForKind reads every known process against the stored version, so
 * a process that has never reported is a row saying so rather than a missing
 * one. Staleness wins over the version comparison: a record that stopped being
 * re-stamped describes a process that may no longer exist, so it can never
 * read as running. */
export function applyStatusForKind(
  records: ApplyStatusRecord[],
  processes: string[],
  kind: string,
  storedVersion: number,
): ApplyStatusRow[] {
  return processes.map((process) => {
    const record = records.find(
      (entry) => entry.process === process && entry.kind === kind,
    );
    if (!record)
      return {
        process,
        state: "not-reporting" as const,
        version: 0,
        error: "",
      };
    const row = {
      process,
      version: record.applied_version,
      error: record.error ?? "",
    };
    if (record.state === "unknown")
      return { ...row, state: "unknown" as const };
    if (record.state === "failed") return { ...row, state: "failed" as const };
    if (record.applied_version < storedVersion)
      return { ...row, state: "pending-restart" as const };
    return { ...row, state: "running" as const };
  });
}

/** One entry of a resource's audit trail. `value` is what that version held,
 * which is also the payload a restore replays through the replace command. */
export interface ResourceRevision {
  version: number;
  value: unknown;
  actor: string;
  source: string;
  request_id: string;
  at?: string;
}

/** One changed field of one record, as sys_audit holds it. */
export interface AuditEntry {
  id: number;
  table: string;
  record_key: string;
  field: string;
  old_value: unknown;
  new_value: unknown;
  version: number;
  actor: string;
  source: string;
  at?: string;
}

interface HistoryResponse {
  revisions?: ResourceRevision[];
}

export interface ResourceState {
  resource?: ControlPlaneResource;
  loading: boolean;
  saving: boolean;
  error?: string;
  conflict: boolean;
  stream: "connecting" | "live" | "reconnecting";
}

class ControlPlaneError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.method && init.method !== "GET") {
    headers.set("Content-Type", "application/json");
    headers.set("X-Archie-CSRF", "1");
  }
  const response = await fetch(path, {
    ...init,
    headers,
    signal: AbortSignal.timeout(15_000),
  });
  if (!response.ok)
    throw new ControlPlaneError(
      (await response.text()).trim() || response.statusText,
      response.status,
    );
  return response.json() as Promise<T>;
}

export type ControlPlanePage =
  "tasks" | "models" | "repositories" | "channels" | "advanced" | "workflows";

const PAGE_RESOURCES: Record<ControlPlanePage, string[]> = {
  tasks: ["workflow-execution-settings"],
  models: ["provider-settings", "model-role-assignments"],
  repositories: ["repository-policies"],
  channels: ["channel-settings"],
  advanced: [
    "personas",
    "schedules",
    "scheduling-policy",
    "tool-settings",
    "plugin-settings",
    "container-runtime-policies",
  ],
  workflows: ["workflow-definitions"],
};

export interface WorkflowDefinitionEntry {
  id: string;
  yaml: string;
}
export interface WorkflowDefinitionCollection {
  definitions: WorkflowDefinitionEntry[];
}

export function cloneControlPlaneValue<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

export function resourcesForPage(
  catalog: ResourceDescriptor[],
  page: ControlPlanePage,
): ResourceDescriptor[] {
  const ownership = PAGE_RESOURCES[page];
  return ownership.flatMap((kind) =>
    catalog.filter((descriptor) => descriptor.kind === kind),
  );
}

export function upsertWorkflowDefinition(
  collection: WorkflowDefinitionCollection,
  entry: WorkflowDefinitionEntry,
): WorkflowDefinitionCollection {
  const found = collection.definitions.some(({ id }) => id === entry.id);
  return {
    definitions: found
      ? collection.definitions.map((definition) =>
          definition.id === entry.id ? entry : definition,
        )
      : [...collection.definitions, entry],
  };
}

export function removeWorkflowDefinition(
  collection: WorkflowDefinitionCollection,
  id: string,
): WorkflowDefinitionCollection {
  return {
    definitions: collection.definitions.filter(
      (definition) => definition.id !== id,
    ),
  };
}

export function commandBody(value: unknown, expectedVersion: number): string {
  return JSON.stringify({ value, expected_version: expectedVersion });
}

interface Draft {
  base: ControlPlaneResource;
  value: unknown;
}

export const useControlPlaneStore = defineStore("control-plane", () => {
  const catalog = ref<ResourceDescriptor[]>([]);
  const catalogError = ref("");
  const states = reactive<Record<string, ResourceState>>({});
  const live = useLiveUpdatesStore();
  const applyStatus = ref<ApplyStatusResponse>({ records: [], processes: [] });

  const genericResources = computed(() =>
    catalog.value.filter(
      (item) =>
        item.apply_mode !== "domain-managed" &&
        !item.query_service &&
        item.commands.includes("replace"),
    ),
  );

  function stateFor(kind: string): ResourceState {
    if (!states[kind])
      states[kind] = {
        loading: false,
        saving: false,
        conflict: false,
        stream: live.streamState === "live" ? "live" : "connecting",
      };
    return states[kind];
  }

  function apply(resource: ControlPlaneResource): void {
    const state = stateFor(resource.kind);
    if (!state.resource || resource.version >= state.resource.version)
      state.resource = resource;
    state.error = undefined;
    state.conflict = false;
    if (changesFor(resource.kind).length === 0) resetDraft(resource.kind);
  }

  // An edit in progress, against the version it started from. A save sends
  // that version, so a change made elsewhere meanwhile is a conflict rather
  // than silently overwritten.
  const drafts = reactive<Record<string, Draft>>({});

  function resetDraft(kind: string): void {
    const resource = stateFor(kind).resource;
    if (resource)
      drafts[kind] = { base: resource, value: cloneControlPlaneValue(resource.value) };
  }

  function changesFor(kind: string): Change[] {
    const draft = drafts[kind];
    return draft ? diffValues(draft.base.value, draft.value) : [];
  }

  const dirtyKinds = computed(() =>
    genericResources.value
      .map(({ kind }) => kind)
      .filter((kind) => changesFor(kind).length > 0),
  );

  async function saveDraft(kind: string): Promise<boolean> {
    const draft = drafts[kind];
    if (!draft) return true;
    const saved = await replace(kind, cloneControlPlaneValue(draft.value), draft.base.version);
    if (saved) resetDraft(kind);
    else if (stateFor(kind).conflict && stateFor(kind).resource)
      drafts[kind] = { base: stateFor(kind).resource!, value: draft.value };
    return saved;
  }

  function saveDrafts() {
    return saveInOrder(dirtyKinds.value, saveDraft);
  }

  async function loadResource(kind: string): Promise<void> {
    const state = stateFor(kind);
    state.loading = true;
    try {
      apply(
        (
          await request<ResourceResponse>(
            `/api/control-plane/resources/${encodeURIComponent(kind)}`,
          )
        ).resource,
      );
    } catch (error) {
      state.error = String((error as Error).message || error);
    } finally {
      state.loading = false;
    }
  }

  const stopResource = live.subscribe("control-plane", (data) => {
    const resource = (data as ResourceResponse).resource;
    if (resource?.kind) apply(resource);
  });
  const stopStatus = live.subscribe("apply-status", (data) => {
    applyStatus.value = data as ApplyStatusResponse;
  });
  const stopConnection = watch(() => live.streamState, (state) => {
    for (const resource of Object.values(states))
      resource.stream = state === "live" ? "live" : "reconnecting";
  });
  onScopeDispose(() => {
    stopResource();
    stopStatus();
    stopConnection();
  });

  /** Re-read on every load and after each save, so the page reflects what the
   * processes did with the edit rather than what was stored. A failure leaves
   * the previous rows: apply status is a diagnostic, and losing it must not
   * take the settings page with it. */
  async function loadApplyStatus(): Promise<void> {
    try {
      applyStatus.value = await request<ApplyStatusResponse>(
        "/api/control-plane/apply-status",
      );
    } catch {
      // Keep whatever was last read.
    }
  }

  /** Rows for one resource, every known process included. */
  function applyStatusFor(kind: string): ApplyStatusRow[] {
    return applyStatusForKind(
      applyStatus.value.records,
      applyStatus.value.processes,
      kind,
      stateFor(kind).resource?.version ?? 0,
    );
  }

  async function load(): Promise<void> {
    try {
      const response = await request<{ resources?: ResourceDescriptor[] }>(
        "/api/control-plane/catalog",
      );
      catalog.value = response.resources ?? [];
      catalogError.value = "";
      await loadApplyStatus();
      await Promise.all(genericResources.value.map(({ kind }) => loadResource(kind)));
    } catch (error) {
      catalogError.value = String((error as Error).message || error);
    }
  }

  async function replace(
    kind: string,
    value: unknown,
    expectedVersion?: number,
  ): Promise<boolean> {
    const state = stateFor(kind);
    if (!state.resource || state.saving) return false;
    state.saving = true;
    state.error = undefined;
    state.conflict = false;
    try {
      const response = await request<ResourceResponse>(
        `/api/control-plane/resources/${encodeURIComponent(kind)}/commands/replace`,
        { method: "POST", body: commandBody(value, expectedVersion ?? state.resource.version) },
      );
      apply(response.resource);
      await loadApplyStatus();
      return true;
    } catch (error) {
      if (error instanceof ControlPlaneError && error.status === 409) {
        state.conflict = true;
        state.error = "Changed elsewhere. Latest values loaded.";
        await loadResource(kind);
      } else {
        state.error = String((error as Error).message || error);
      }
      return false;
    } finally {
      state.saving = false;
    }
  }

  async function history(kind: string): Promise<ResourceRevision[]> {
    const response = await request<HistoryResponse>(
      `/api/control-plane/resources/${encodeURIComponent(kind)}/history`,
    );
    return response.revisions ?? [];
  }

  async function audit(table: string, keys: string[]): Promise<AuditEntry[]> {
    const query = new URLSearchParams({ table });
    for (const key of keys) query.append("key", key);
    const response = await request<{ entries?: AuditEntry[] }>(
      `/api/control-plane/audit?${query}`,
    );
    return response.entries ?? [];
  }

  /** The shipped definitions the catalog offers as "restore shipped". */
  function shippedWorkflows(): WorkflowDefinitionCollection {
    const defaults = catalog.value.find(
      (resource) => resource.kind === "workflow-definitions",
    )?.defaults_json;
    if (!defaults) return { definitions: [] };
    try {
      return JSON.parse(defaults) as WorkflowDefinitionCollection;
    } catch {
      return { definitions: [] };
    }
  }

  return {
    applyStatusFor,
    changesFor,
    dirtyKinds,
    drafts,
    resetDraft,
    saveDrafts,
    catalog,
    catalogError,
    states,
    genericResources,
    audit,
    history,
    load,
    loadApplyStatus,
    replace,
    shippedWorkflows,
    stateFor,
  };
});
