<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api } from "@/lib/api";

interface AppliedRecord {
  kind: string;
  applied_version: number;
  error?: string;
  state: "current" | "failed" | "unknown";
}

interface Service {
  service: string;
  instance_id: string;
  state: "up" | "degraded" | "down" | "unknown";
  version: string;
  install_type: string;
  started_at: string;
  reported_at: string;
  detail?: string;
  applied: AppliedRecord[];
  checks?: { name: string; status: "ok" | "degraded"; detail?: string }[];
}

const services = ref<Service[]>([]);
const error = ref<string | null>(null);
let timer: ReturnType<typeof setInterval> | undefined;

const tone = { up: "ok", degraded: "warn", down: "danger", unknown: "idle" } as const;

function uptime(service: Service): string {
  if (!service.started_at || service.state === "down") return "";
  const seconds = Math.max(
    0,
    Math.round((Date.now() - Date.parse(service.started_at)) / 1000),
  );
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days) return `${days}d ${hours}h`;
  if (hours) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

async function load(): Promise<void> {
  try {
    services.value = await api.services<Service[]>();
    error.value = null;
  } catch (err) {
    error.value = String((err as Error).message || err);
  }
}

onMounted(() => {
  void load();
  timer = setInterval(() => void load(), 15_000);
});
onUnmounted(() => clearInterval(timer));
</script>

<template>
  <Card class="mb-4">
    <CardHeader>
      <CardTitle>Services</CardTitle>
    </CardHeader>
    <CardContent>
      <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      <Table v-else>
        <TableHeader>
          <TableRow>
            <TableHead>Service</TableHead>
            <TableHead>Version</TableHead>
            <TableHead>Uptime</TableHead>
            <TableHead>Last seen</TableHead>
            <TableHead>Applied</TableHead>
            <TableHead class="w-px text-right">State</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="service in services" :key="`${service.service}/${service.instance_id}`">
            <TableCell>
              <span class="font-mono font-medium">{{ service.service }}</span>
              <span v-if="service.service === 'archie-agent'" class="ml-2 text-sm text-fg-muted">task {{ service.instance_id }}</span>
              <span v-if="service.detail" class="ml-2 text-sm text-fg-muted"
                >{{ service.state }}: {{ service.detail }}</span
              >
              <div v-for="check in service.checks" :key="check.name" class="mt-1 flex items-center gap-2 text-xs">
                <span>{{ check.name }}</span>
                <Badge :variant="check.status === 'ok' ? 'ok' : 'warn'">{{ check.status }}</Badge>
                <span v-if="check.detail" class="text-fg-muted">{{ check.detail }}</span>
              </div>
            </TableCell>
            <TableCell class="text-sm">
              <template v-if="service.version">
                {{ service.version }}
                <span class="text-fg-muted">{{ service.install_type }}</span>
              </template>
            </TableCell>
            <TableCell class="text-sm">{{ uptime(service) }}</TableCell>
            <TableCell class="text-xs">{{ service.reported_at && new Date(service.reported_at).getFullYear() > 1 ? new Date(service.reported_at).toLocaleString() : "" }}</TableCell>
            <TableCell class="text-xs">
              <div v-for="record in service.applied" :key="record.kind">
                <span class="font-mono">{{ record.kind }}</span>
                v{{ record.applied_version }}
                <span v-if="record.error" class="text-destructive">{{
                  record.error
                }}</span>
              </div>
            </TableCell>
            <TableCell class="w-px text-right">
              <Badge :variant="tone[service.state]">{{ service.service === "archie-agent" && service.state === "down" ? "stale" : service.state }}</Badge>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </CardContent>
  </Card>
</template>
