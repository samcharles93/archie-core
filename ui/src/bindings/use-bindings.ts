import { ref } from "vue";

import { api, ApiError } from "@/lib/api";
import type { EventType } from "@/captures/event-types";
import {
  bindingPayload,
  type Binding,
  type BindingDraft,
  type MappingOption,
  type WorkflowOption,
} from "./binding-draft";

/**
 * Why the page has nothing to show. "unconfigured" is a daemon with no
 * bindings store wired (501/503) -- the feature is off, which reads very
 * differently from a daemon that answered badly.
 */
export type LoadFailureKind = "unconfigured" | "failed";

export interface LoadFailure {
  kind: LoadFailureKind;
  message: string;
}

function failureFor(err: unknown): LoadFailure {
  const message = err instanceof Error && err.message ? err.message : String(err);
  const status = err instanceof ApiError ? err.status : undefined;
  return { kind: status === 501 || status === 503 ? "unconfigured" : "failed", message };
}

/**
 * The bindings page's data: the three lists the list and the editor read, the
 * load that fills them, and the three mutations. Every mutation goes through
 * api.* so the CSRF and Content-Type headers are sent -- a bare fetch is
 * refused 403/415 by archied's mutation guard.
 */
export function useBindings() {
  // null means loading, [] means loaded and empty.
  const bindings = ref<Binding[] | null>(null);
  const mappings = ref<MappingOption[]>([]);
  const eventTypes = ref<EventType[]>([]);
  const workflows = ref<WorkflowOption[]>([]);
  const failure = ref<LoadFailure | null>(null);
  // A failed mutation is not a failed load: the list on screen is still
  // accurate, so it keeps rendering and the failure lands beside it.
  const actionFailure = ref<LoadFailure | null>(null);
  const saving = ref(false);
  const saveFailure = ref<string | null>(null);

  async function load(): Promise<void> {
    failure.value = null;
    try {
      const [b, m, w, t] = await Promise.all([
        api.bindings<{ bindings?: Binding[] }>(),
        api.mappings<{ mappings?: MappingOption[] }>(),
        api.workflows<{ definitions?: WorkflowOption[] }>(),
        api.eventTypes<{ event_types?: EventType[] }>(),
      ]);
      bindings.value = b.bindings || [];
      mappings.value = m.mappings || [];
      eventTypes.value = t.event_types || [];
      // Only enabled definitions are offerable: binding to a disabled one
      // would persist a binding that can never dispatch.
      workflows.value = (w.definitions || []).filter((d) => d.enabled);
    } catch (err) {
      failure.value = failureFor(err);
      bindings.value = null;
    }
  }

  function resetSaveFailure(): void {
    saveFailure.value = null;
  }

  /** True when the binding was written, so the caller knows to close. */
  async function save(draft: BindingDraft): Promise<boolean> {
    saving.value = true;
    saveFailure.value = null;
    try {
      const body = bindingPayload(draft, eventTypes.value);
      if (draft.id) {
        await api.bindingUpdate(String(draft.id), body);
      } else {
        await api.bindingCreate(body);
      }
      await load();
      return true;
    } catch (err) {
      saveFailure.value = err instanceof Error && err.message ? err.message : String(err);
      return false;
    } finally {
      saving.value = false;
    }
  }

  async function approve(binding: Binding): Promise<void> {
    actionFailure.value = null;
    try {
      await api.bindingApprove(String(binding.id));
      await load();
    } catch (err) {
      actionFailure.value = failureFor(err);
    }
  }

  async function remove(binding: Binding): Promise<void> {
    actionFailure.value = null;
    try {
      await api.bindingDelete(String(binding.id));
      await load();
    } catch (err) {
      actionFailure.value = failureFor(err);
    }
  }

  return {
    bindings,
    mappings,
    eventTypes,
    workflows,
    failure,
    actionFailure,
    saving,
    saveFailure,
    load,
    resetSaveFailure,
    save,
    approve,
    remove,
  };
}
