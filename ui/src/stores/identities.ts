import { defineStore } from "pinia";
import { onScopeDispose, ref } from "vue";
import { useLiveUpdatesStore } from "./live-updates";

export interface Identity {
  id: string;
  kind: "system" | "bot" | "service_account" | "user";
  display_name: string;
  lifecycle: "active" | "suspended" | "retired";
  version: number;
}

async function call<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      "X-Archie-CSRF": "1",
      ...init.headers,
    },
  });
  if (!response.ok) throw new Error((await response.text()).trim());
  return response.json() as Promise<T>;
}

export const useIdentitiesStore = defineStore("identities", () => {
  const identities = ref<Identity[]>([]);
  const error = ref("");
  const busy = ref("");
  const live = useLiveUpdatesStore();
  let stop: (() => void) | undefined;

  async function load(): Promise<void> {
    try {
      identities.value = (
        await call<{ identities: Identity[] }>("/api/identities")
      ).identities;
      error.value = "";
    } catch (cause) {
      error.value = String((cause as Error).message || cause);
    }
  }
  function watch(): void {
    if (stop) return;
    void load();
    stop = live.subscribe("identities", (data) => {
      identities.value = (data as { identities: Identity[] }).identities;
      error.value = "";
    });
  }
  async function create(
    display_name: string,
    kind: Identity["kind"],
  ): Promise<boolean> {
    return mutate("create", () =>
      call("/api/identities", {
        method: "POST",
        body: JSON.stringify({ display_name, kind }),
      }),
    );
  }
  async function command(
    value: Identity,
    commandName: string,
    display_name = "",
  ): Promise<boolean> {
    return mutate(value.id, () =>
      call(`/api/identities/${encodeURIComponent(value.id)}/${commandName}`, {
        method: "POST",
        body: JSON.stringify({ expected_version: value.version, display_name }),
      }),
    );
  }
  async function mutate(
    key: string,
    action: () => Promise<unknown>,
  ): Promise<boolean> {
    busy.value = key;
    try {
      await action();
      await load();
      return true;
    } catch (cause) {
      error.value = String((cause as Error).message || cause);
      return false;
    } finally {
      busy.value = "";
    }
  }
  onScopeDispose(() => stop?.());
  return { identities, error, busy, watch, create, command };
});
