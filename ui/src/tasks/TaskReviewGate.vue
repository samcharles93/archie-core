<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import TaskRowActions from "./TaskRowActions.vue";
import type { Task } from "./TaskRow.vue";
import { reviewGateOffer } from "./review-gate";

const props = defineProps<{ task: Task }>();
const emit = defineEmits<{ done: [taskId: Task["id"]] }>();
const gate = computed(() => props.task.status === "waiting_human" ? reviewGateOffer(props.task.review_gate) : null);
const selected = ref<string[]>([]);
watch(() => props.task.review_gate, () => {
  selected.value = gate.value?.findings.map((entry) => entry.key) ?? [];
}, { immediate: true });
</script>

<template>
  <Card v-if="gate" class="mb-6">
    <CardHeader>
      <CardTitle>Review findings</CardTitle>
      <p v-if="gate.head_sha" class="break-all font-mono text-xs text-muted-foreground">Head commit: {{ gate.head_sha }}</p>
    </CardHeader>
    <CardContent class="space-y-4">
      <ul class="divide-y divide-border rounded-md border border-border">
        <li v-for="entry in gate.findings" :key="entry.key" class="space-y-2 p-4">
          <label class="flex cursor-pointer items-start gap-3">
            <input v-model="selected" type="checkbox" :value="entry.key" :aria-label="entry.finding.Title" class="mt-1 shrink-0" />
            <span class="min-w-0 space-y-1">
              <span class="block text-sm font-medium">{{ entry.finding.Title }}</span>
              <span class="block break-all font-mono text-xs text-muted-foreground">{{ entry.finding.File }}<template v-if="entry.finding.LineStart">:{{ entry.finding.LineStart }}<template v-if="entry.finding.LineEnd > entry.finding.LineStart">–{{ entry.finding.LineEnd }}</template></template></span>
            </span>
          </label>
          <div class="flex flex-wrap items-center gap-2 text-xs">
            <Badge variant="outline">{{ entry.finding.Severity }}</Badge>
            <Badge v-if="entry.finding.Blocking" variant="danger">Blocking</Badge>
            <span>Score: {{ entry.finding.Score }}</span>
            <span>Confidence: {{ entry.finding.Confidence }}</span>
          </div>
          <p class="whitespace-pre-wrap break-words text-sm">{{ entry.finding.Body }}</p>
          <p v-if="entry.finding.Evidence" class="whitespace-pre-wrap break-words text-xs text-muted-foreground">Evidence: {{ entry.finding.Evidence }}</p>
          <pre v-if="entry.finding.Suggestion" class="overflow-x-auto rounded-md bg-secondary p-2 text-xs">{{ entry.finding.Suggestion }}</pre>
        </li>
      </ul>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-xs text-muted-foreground">{{ selected.length }} of {{ gate.findings.length }} selected</p>
        <TaskRowActions :task="task" :only="['approve', 'rereview', 'reject']" :findings="selected" @done="emit('done', $event)" />
      </div>
    </CardContent>
  </Card>
</template>
