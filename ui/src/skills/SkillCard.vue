<script lang="ts">
/** One discovered skill, as /api/skills serialises it. */
export interface Skill {
  name?: string;
  description?: string;
  workflow?: string;
  source?: string;
}
</script>

<script setup lang="ts">
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

/** One catalogue entry: what it is, and where it came from. */
const props = defineProps<{ skill: Skill }>();
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>{{ props.skill.name || "Untitled skill" }}</CardTitle>
      <CardDescription>{{ props.skill.description || "No description provided." }}</CardDescription>
    </CardHeader>
    <CardContent>
      <div class="flex items-center justify-between gap-2">
        <span class="text-xs text-fg-subtle">{{ props.skill.source || "Unknown source" }}</span>
        <Badge v-if="props.skill.workflow" variant="info">{{ props.skill.workflow }}</Badge>
      </div>
    </CardContent>
  </Card>
</template>
