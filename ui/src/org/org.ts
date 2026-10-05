import { ref } from "vue";

import { api } from "@/lib/api";

/** The org the dashboard is scoped to. GET /api/orgs returns only it. */
export interface Org {
  id: string;
  name: string;
}

export interface OrgWorkspace {
  id: string;
  org_id: string;
  name: string;
  environment?: string;
}

export type Role = "owner" | "admin" | "developer" | "viewer";

/** The roles the domain accepts, in descending authority. */
export const ROLES: Role[] = ["owner", "admin", "developer", "viewer"];

export interface Member {
  identity_id: string;
  kind: string;
  display_name: string;
  workspace_id?: string;
  role: Role;
}

// The org is read once and shared: every Org tab draws the same header, and
// the shell remounts between tab routes, so a per-mount fetch would refetch
// and flash the header on every switch.
const current = ref<Org | null>(null);
let pending: Promise<void> | null = null;

export function loadOrg(): Promise<void> {
  if (current.value) return Promise.resolve();
  if (pending) return pending;
  pending = api
    .orgs<{ orgs: Org[] }>()
    .then((response) => {
      current.value = response.orgs[0] ?? null;
    })
    .finally(() => {
      pending = null;
    });
  return pending;
}

export const org = current;
