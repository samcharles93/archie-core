import { defineStore } from "pinia";
import { ref } from "vue";

export interface Authority {
  credential_services: string[];
  egress_hosts: string[];
  forge_permissions: string[];
  triggers: string[];
  tools: string[];
  env: string[];
}

export interface Extension {
  name: string;
  display_name: string;
  description: string;
  version: string;
  reference: string;
  digest: string;
  surfaces: string[];
  declared: Authority;
  accepted: Authority | null;
  enabled: boolean;
}

/** A package the verified catalogue offers. */
export interface CatalogueEntry {
  name: string;
  surface: string;
  version: string;
  description: string;
  reference: string;
  digest: string;
  installed: boolean;
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

/** The grants an authority record names, as "kind: value" lines. */
export function grants(authority: Authority): string[] {
  const labelled: [string, string[]][] = [
    ["credential", authority.credential_services],
    ["egress", authority.egress_hosts],
    ["forge", authority.forge_permissions],
    ["trigger", authority.triggers],
    ["tool", authority.tools],
    ["env", authority.env],
  ];
  return labelled.flatMap(([label, values]) => values.map((value) => `${label}: ${value}`));
}

/** What an extension needs from the operator next, in the order it happens. */
export function stage(extension: Extension): "accept" | "disabled" | "enabled" | "inert" {
  if (!extension.surfaces.length) return "inert";
  if (!extension.accepted) return "accept";
  return extension.enabled ? "enabled" : "disabled";
}

export const useExtensionsStore = defineStore("extensions", () => {
  const extensions = ref<Extension[]>([]);
  const catalogue = ref<CatalogueEntry[]>([]);
  const catalogueError = ref("");
  const error = ref("");
  const busy = ref("");

  async function load(): Promise<void> {
    try {
      extensions.value = (await call<{ extensions: Extension[] }>("/api/extensions")).extensions;
      error.value = "";
    } catch (cause) {
      error.value = String((cause as Error).message || cause);
    }
    // The catalogue is remote; its failure leaves the installed list usable.
    try {
      catalogue.value = await call<CatalogueEntry[]>("/api/extensions/catalogue");
      catalogueError.value = "";
    } catch (cause) {
      catalogueError.value = String((cause as Error).message || cause);
    }
  }
  async function mutate(key: string, action: () => Promise<unknown>): Promise<boolean> {
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
  const path = (name: string) => `/api/extensions/${encodeURIComponent(name)}`;
  return {
    extensions,
    catalogue,
    catalogueError,
    error,
    busy,
    load,
    install: (name: string, reference: string, digest: string) =>
      mutate("install", () => call("/api/extensions", { method: "POST", body: JSON.stringify({ name, reference, digest }) })),
    installFromCatalogue: (name: string) =>
      mutate(name, () => call(`/api/extensions/catalogue/${encodeURIComponent(name)}/install`, { method: "POST" })),
    accept: (name: string) => mutate(name, () => call(`${path(name)}/accept`, { method: "POST" })),
    setEnabled: (name: string, enabled: boolean) =>
      mutate(name, () => call(`${path(name)}/enabled`, { method: "PUT", body: JSON.stringify({ enabled }) })),
    remove: (name: string) => mutate(name, () => call(path(name), { method: "DELETE" })),
  };
});
