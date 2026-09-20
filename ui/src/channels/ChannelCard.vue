<script lang="ts">
/** A conversational front-end, as /api/channels serialises it. */
export interface Channel {
  id?: string;
  name: string;
  state?: string;
  configured?: boolean;
  description?: string;
  detail?: string;
  reload_supported?: boolean;
}
</script>

<script setup lang="ts">
import { computed } from "vue";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { StatusKind } from "@/lib/status";

const props = defineProps<{ channel: Channel }>();

function tone(state: string | undefined, configured: boolean | undefined): StatusKind {
  if (state === "running") return "ok";
  if (state === "failed" || state === "degraded") return "warn";
  return configured ? "idle" : "idle";
}

const label = computed(() => props.channel.state || (props.channel.configured ? "configured" : "stopped"));
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>{{ props.channel.name }}</CardTitle>
      <CardDescription v-if="props.channel.description">{{ props.channel.description }}</CardDescription>
      <Badge :variant="tone(props.channel.state, props.channel.configured)">{{ label }}</Badge>
    </CardHeader>
    <CardContent>
      <p v-if="props.channel.detail" class="text-sm text-fg-muted">{{ props.channel.detail }}</p>
    </CardContent>
  </Card>
</template>
