<script setup lang="ts">
import { Badge } from "@/components/ui/badge";
import { statusKind, statusLabel } from "./binding-draft";

/** A binding's lifecycle state: draft, pending approval, or armed. The
 * colour ramp is shared with every other status in the dashboard, so the
 * same colour means the same thing here as it does on a task. */
const props = defineProps<{ status?: string }>();
</script>

<template>
  <Badge :variant="statusKind(props.status)">{{ statusLabel(props.status) }}</Badge>
</template>
