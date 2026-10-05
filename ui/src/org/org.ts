import { ref } from "vue";

import { api } from "@/lib/api";

/** An org on the instance. A non-admin only ever sees their own. */
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

// The org list is read once and shared: every Org tab draws the same header,
// and the shell remounts between tab routes, so a per-mount fetch would
// refetch and flash the header on every switch.
export const orgs = ref<Org[]>([]);
export const instanceAdmin = ref(false);
/** The org the caller acts in; Access and Tokens always read this one. */
export const homeOrg = ref("");
const current = ref<Org | null>(null);
let pending: Promise<void> | null = null;

interface OrgList {
  orgs: Org[];
  current: string;
  instance_admin: boolean;
}

/** loadOrg reads the org list; reload refetches it after an org is created. */
export function loadOrg(reload = false): Promise<void> {
  if (current.value && !reload) return Promise.resolve();
  if (pending) return pending;
  pending = api
    .orgs<OrgList>()
    .then((response) => {
      orgs.value = response.orgs;
      instanceAdmin.value = response.instance_admin;
      homeOrg.value = response.current;
      const keep = current.value?.id ?? response.current;
      current.value = response.orgs.find((o) => o.id === keep) ?? response.orgs[0] ?? null;
    })
    .finally(() => {
      pending = null;
    });
  return pending;
}

/** viewOrg points the Org area at another org; only an instance admin lists more than one. */
export function viewOrg(id: string): void {
  current.value = orgs.value.find((o) => o.id === id) ?? current.value;
}

export const org = current;
