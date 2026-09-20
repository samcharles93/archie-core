<script setup lang="ts">
import { Button } from "@/components/ui/button";
import { decideDangerous } from "./state";
import type { DangerousAction } from "./types";

/**
 * One queued request: what it would do, and the decisions open on it. Approve
 * runs it once, "Approve for 24h" runs it for a day, and Deny drops it.
 */
const props = defineProps<{ action: DangerousAction }>();
</script>

<template>
  <div class="flex flex-wrap items-center justify-between gap-3 border-t border-hairline py-3">
    <span class="text-sm">{{ props.action.description }}</span>
    <div class="flex flex-wrap gap-2">
      <Button variant="outline" @click="decideDangerous(props.action.id, 'approve')">Approve</Button>
      <Button variant="outline" @click="decideDangerous(props.action.id, 'permanent')">Approve for 24h</Button>
      <Button variant="outline" @click="decideDangerous(props.action.id, 'deny')">Deny</Button>
    </div>
  </div>
</template>
