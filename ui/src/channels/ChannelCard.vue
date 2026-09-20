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
import { RefreshCw } from "@lucide/vue";
import { computed, ref } from "vue";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { StatusKind } from "@/lib/status";

const props = defineProps<{ channel: Channel }>();
const emit = defineEmits<{ reload: [id: string] }>();

// In-flight state rather than mutating the button's disabled attribute: the
// element is re-rendered by Vue, so a DOM write would be discarded.
const reloading = ref(false);

function tone(state: string | undefined, configured: boolean | undefined): StatusKind {
  if (state === "running") return "ok";
  if (state === "failed" || state === "degraded") return "warn";
  return configured ? "idle" : "idle";
}

const label = computed(() => props.channel.state || (props.channel.configured ? "configured" : "stopped"));

async function reload() {
  reloading.value = true;
  try {
    emit("reload", props.channel.id || props.channel.name.toLowerCase());
  } finally {
    reloading.value = false;
  }
}
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>{{ props.channel.name }}</CardTitle>
      <CardDescription v-if="props.channel.description">{{ props.channel.description }}</CardDescription>
      <Badge :variant="tone(props.channel.state, props.channel.configured)">{{ label }}</Badge>
    </CardHeader>
    <CardContent>
      <p v-if="props.channel.detail" class="mb-3 text-sm text-fg-muted">{{ props.channel.detail }}</p>
      <Button v-if="props.channel.reload_supported" variant="outline" :disabled="reloading" @click="reload">
        <RefreshCw data-icon="inline-start" />
        Reload
      </Button>
      <p v-else class="text-sm text-fg-subtle">Reload requires a daemon restart for this adapter.</p>
    </CardContent>
  </Card>
</template>
