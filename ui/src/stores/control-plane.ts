import { defineStore } from "pinia";
import { computed, reactive, ref } from "vue";

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

export const useControlPlaneStore = defineStore("control-plane", () => {
  const catalog = ref<ResourceDescriptor[]>([]);
  const catalogError = ref("");
  const states = reactive<Record<string, ResourceState>>({});
  const streams = new Map<string, EventSource>();
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
        stream: "connecting",
      };
    return states[kind];
  }

  function apply(resource: ControlPlaneResource): void {
    const state = stateFor(resource.kind);
    if (!state.resource || resource.version >= state.resource.version)
      state.resource = resource;
    state.error = undefined;
    state.conflict = false;
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

  function watchResource(kind: string): void {
    if (streams.has(kind)) return;
    const state = stateFor(kind);
    const after = state.resource?.version ?? 0;
    const stream = new EventSource(
      `/api/control-plane/watch/${encodeURIComponent(kind)}?after=${after}`,
    );
    stream.onopen = () => {
      state.stream = "live";
    };
    stream.onerror = () => {
      state.stream = "reconnecting";
    };
    stream.onmessage = (event) => {
      try {
        apply((JSON.parse(event.data) as ResourceResponse).resource);
      } catch {
        state.error = "Invalid live update";
      }
    };
    streams.set(kind, stream);
  }

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
      await Promise.all(
        genericResources.value.map(async ({ kind }) => {
          await loadResource(kind);
          watchResource(kind);
        }),
      );
    } catch (error) {
      catalogError.value = String((error as Error).message || error);
    }
  }

  async function replace(kind: string, value: unknown): Promise<boolean> {
    const state = stateFor(kind);
    if (!state.resource || state.saving) return false;
    state.saving = true;
    state.error = undefined;
    state.conflict = false;
    try {
      const response = await request<ResourceResponse>(
        `/api/control-plane/resources/${encodeURIComponent(kind)}/commands/replace`,
        { method: "POST", body: commandBody(value, state.resource.version) },
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
    catalog,
    catalogError,
    states,
    genericResources,
    history,
    load,
    loadApplyStatus,
    replace,
    shippedWorkflows,
    stateFor,
  };
});
