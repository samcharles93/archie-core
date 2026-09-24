<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ApiError, api } from "@/lib/api";

/**
 * Per-component versions and the update that applies them.
 *
 * Everything behind this card already exists: /api/version reports what is
 * running, what the check command claims, what is available, and the honest
 * status between those (ok, update_available, drift, unknown); the install
 * endpoint re-validates freshness and refuses a stale snapshot. This card
 * only surfaces them.
 */

interface ComponentVersion {
  id: string;
  label: string;
  install_type?: string;
  reference?: string;
  running_version?: string;
  installed_claim?: string;
  latest_available?: string;
  status: "ok" | "update_available" | "drift" | "unknown";
}

interface ComponentSnapshot {
  id?: string;
  label?: string;
  installed?: string;
  available?: string;
}

interface UpdateSnapshot {
  components?: ComponentSnapshot[];
  deferred?: boolean;
}

const components = ref<ComponentVersion[]>([]);
const snapshot = ref<UpdateSnapshot | null>(null);
const canInstall = ref(false);
const notConfigured = ref(false);
const loading = ref(true);
const loadError = ref<string | null>(null);
const installing = ref(false);
const installProgress = ref<string[]>([]);
const installResult = ref<string | null>(null);
const installError = ref<string | null>(null);

const updateable = computed(() =>
  components.value.filter((c) => c.status === "update_available"),
);

const statusKind: Record<
  ComponentVersion["status"],
  "ok" | "warn" | "danger" | "idle"
> = {
  ok: "ok",
  update_available: "warn",
  drift: "danger",
  unknown: "idle",
};

const statusLabel: Record<ComponentVersion["status"], string> = {
  ok: "Up to date",
  update_available: "Update available",
  drift: "Version drift",
  unknown: "Unknown",
};

async function load(): Promise<void> {
  loading.value = true;
  loadError.value = null;
  try {
    const [report, update] = await Promise.all([
      api.version<{ components: ComponentVersion[] }>(),
      api
        .updateSnapshot<{ snapshot: UpdateSnapshot; can_install: boolean }>()
        .catch((err) => {
          if (err instanceof ApiError && err.status === 501) {
            notConfigured.value = true;
            return null;
          }
          throw err;
        }),
    ]);
    components.value = report.components;
    if (update) {
      snapshot.value = update.snapshot;
      canInstall.value = update.can_install;
    }
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

// The snapshot the install endpoint demands is the one the operator saw —
// the endpoint re-checks freshness and answers 409 when releases moved.
function installableSnapshot(): UpdateSnapshot | null {
  if (!snapshot.value) return null;
  if (
    snapshot.value.components?.some(
      (c) => c.available && c.available !== c.installed,
    )
  ) {
    return snapshot.value;
  }
  return null;
}

async function install(): Promise<void> {
  const snap = installableSnapshot();
  if (!snap || installing.value) return;
  installing.value = true;
  installError.value = null;
  installProgress.value = [];
  installResult.value = null;
  try {
    const result = await api.updateInstall<{
      progress?: string[];
      result?: { installed?: Record<string, string> };
    }>(snap);
    installProgress.value = result.progress ?? [];
    const installed = result.result?.installed;
    installResult.value = installed
      ? Object.entries(installed)
          .map(([id, version]) => `${id} → ${version}`)
          .join(", ")
      : "update finished";
    await load();
  } catch (err) {
    installError.value = err instanceof Error ? err.message : String(err);
  } finally {
    installing.value = false;
  }
}

onMounted(load);
</script>

<template>
  <p v-if="notConfigured" class="text-sm text-fg-muted">
    Update checks not configured.
  </p>
  <Card v-else>
    <CardHeader>
      <CardTitle>Versions</CardTitle>
    </CardHeader>
    <CardContent>
      <p v-if="loadError" class="text-sm text-danger">{{ loadError }}</p>
      <p v-else-if="loading" class="text-sm text-fg-muted">Checking…</p>
      <template v-else>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Component</TableHead>
              <TableHead>Running</TableHead>
              <TableHead>Available</TableHead>
              <TableHead>Status</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="component in components" :key="component.id">
              <TableCell class="font-medium">{{
                component.label || component.id
              }}</TableCell>
              <TableCell class="font-mono text-fg-muted">{{
                component.running_version || component.installed_claim || "—"
              }}</TableCell>
              <TableCell class="font-mono text-fg-muted">{{
                component.latest_available || "—"
              }}</TableCell>
              <TableCell>
                <Badge :variant="statusKind[component.status]">{{
                  statusLabel[component.status]
                }}</Badge>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>

        <div
          v-if="canInstall && updateable.length"
          class="mt-4 flex flex-col gap-2"
        >
          <Button :disabled="installing" @click="install">
            {{
              installing
                ? "Installing…"
                : `Install ${updateable.map((c) => c.label || c.id).join(", ")}`
            }}
          </Button>
          <p v-if="installing" class="text-xs text-fg-muted">
            The build runs on this host and the restart is verified out of band;
            the page can be left.
          </p>
        </div>

        <div
          v-if="installProgress.length"
          class="mt-3 rounded-sm border border-border bg-muted p-3 font-mono text-xs"
        >
          <p v-for="(line, i) in installProgress" :key="i">{{ line }}</p>
        </div>
        <p v-if="installResult" class="mt-2 text-sm text-ok">
          {{ installResult }}
        </p>
        <p v-if="installError" class="mt-2 text-sm text-danger">
          {{ installError }}
        </p>
      </template>
    </CardContent>
  </Card>
</template>
