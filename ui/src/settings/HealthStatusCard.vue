<script setup lang="ts">
import { storeToRefs } from "pinia";
import { computed, onMounted, ref } from "vue";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import { useLiveResource, useLiveUpdatesStore } from "@/stores/live-updates";

interface HealthComponent {
  name: string;
  status: "ok" | "degraded";
  ready: boolean;
  detail?: string;
}

interface HealthReport {
  status: "ok" | "degraded";
  components: HealthComponent[];
}

const report = ref<HealthReport | null>(null);
const error = ref<string | null>(null);
const { streamState } = storeToRefs(useLiveUpdatesStore());

const liveState = computed(() => {
  if (streamState.value === "live")
    return { label: "Connected", kind: "ok" as const };
  if (streamState.value === "unavailable")
    return { label: "Unavailable", kind: "danger" as const };
  return { label: "Connecting", kind: "warn" as const };
});

const names: Record<string, string> = {
  state_db: "State store",
  gateway: "Gateway",
  active_model: "Active model",
};

function componentName(name: string): string {
  return (
    names[name] ??
    name.replaceAll("_", " ").replace(/^./, (letter) => letter.toUpperCase())
  );
}

async function load(): Promise<void> {
  try {
    report.value = await api.health<HealthReport>();
    error.value = null;
  } catch (err) {
    error.value = String((err as Error).message || err);
  }
}

useLiveResource(null, () => void load());
onMounted(load);
</script>

<template>
  <Card class="mb-4">
    <CardHeader>
      <CardTitle>Runtime</CardTitle>
    </CardHeader>
    <CardContent>
      <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      <Table v-else>
        <TableBody>
          <TableRow>
            <TableCell class="font-medium">Live updates</TableCell>
            <TableCell class="w-px text-right">
              <Badge :variant="liveState.kind">{{ liveState.label }}</Badge>
            </TableCell>
          </TableRow>
          <TableRow
            v-for="component in report?.components ?? []"
            :key="component.name"
          >
            <TableCell>
              <span class="font-medium">{{
                componentName(component.name)
              }}</span>
              <span
                v-if="component.detail"
                class="ml-2 text-sm text-fg-muted"
                >{{ component.detail }}</span
              >
            </TableCell>
            <TableCell class="w-px text-right">
              <Badge :variant="component.ready ? 'ok' : 'danger'">
                {{ component.ready ? "Ready" : "Degraded" }}
              </Badge>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </CardContent>
  </Card>
</template>
