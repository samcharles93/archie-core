<script lang="ts">
import type { StatusKind } from "@/lib/status";
import type { CuratorActionEntry } from "./CuratorAction.vue";

/** A registered curator, its point-in-time health, and its recent activity. */
export interface Curator {
  name: string;
  health?: { status?: string; message?: string };
  last_run_at?: string | number;
  last_run_actions?: number;
  recent_actions?: CuratorActionEntry[];
}
</script>

<script setup lang="ts">
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ago } from "@/lib/format";
import CuratorAction from "./CuratorAction.vue";

const props = defineProps<{ curator: Curator }>();

function healthKind(status: string | undefined): StatusKind {
  switch (status) {
    case "healthy":
      return "ok";
    case "degraded":
      return "warn";
    case "unhealthy":
      return "danger";
    default:
      return "idle";
  }
}
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>{{ props.curator.name }}</CardTitle>
      <CardDescription>
        {{
          props.curator.last_run_at
            ? `Last ran ${ago(props.curator.last_run_at)} · ${props.curator.last_run_actions} action${props.curator.last_run_actions === 1 ? "" : "s"}`
            : "Has not run yet"
        }}
      </CardDescription>
      <Badge :variant="healthKind(props.curator.health?.status)">
        {{ props.curator.health?.status || "unknown" }}
      </Badge>
    </CardHeader>
    <CardContent>
      <p v-if="props.curator.health?.message" class="mb-3 text-sm text-fg-muted">
        {{ props.curator.health.message }}
      </p>
      <ul v-if="props.curator.recent_actions?.length" class="flex flex-col gap-2">
        <CuratorAction v-for="(a, i) in props.curator.recent_actions" :key="i" :action="a" />
      </ul>
      <p v-else class="text-sm text-fg-subtle">No recorded activity yet.</p>
    </CardContent>
  </Card>
</template>
