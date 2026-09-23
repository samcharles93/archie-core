<script setup lang="ts">
import { Check } from "@lucide/vue";
import { computed, ref, watch } from "vue";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { setupPanelState, type SetupPanelState } from "./setup-preference";
import { setup } from "./state";

/**
 * The setup checklist, rendered only while there is setup work left.
 *
 * A fully configured daemon renders nothing: the old completion card ("Setup
 * complete", with a check and a dismiss button) celebrated the absence of a
 * problem and occupied a grid cell until dismissed. Setup regressing to
 * incomplete brings the checklist back on its own.
 */
const panel = ref<SetupPanelState>(setupPanelState(setup.value));

watch(setup, () => {
  panel.value = setupPanelState(setup.value);
});

const steps = computed(() => setup.value?.steps ?? []);
const pct = computed(() => {
  const total = steps.value.length;
  if (!total) return 0;
  return Math.round(((total - panel.value.remaining.length) / total) * 100);
});
</script>

<template>
  <Card v-if="panel.kind === 'incomplete'">
    <CardHeader>
      <CardTitle>Finish setting up</CardTitle>
      <CardDescription>Archie needs these before it can work on its own.</CardDescription>
      <CardAction>
        <span class="text-lg font-semibold text-link">{{ pct }}%</span>
      </CardAction>
    </CardHeader>
    <CardContent>
      <Progress :model-value="pct" class="mb-4" />
      <ul class="flex flex-col gap-3">
        <li v-for="(step, i) in steps" :key="i" class="flex items-start gap-3">
          <span
            class="mt-px grid size-[18px] flex-none place-items-center rounded-full border-[1.5px] border-border-strong text-xs text-fg-subtle"
            :class="step.done ? 'border-transparent bg-ok-soft text-ok' : ''"
          >
            <Check v-if="step.done" class="size-3" />
          </span>
          <div>
            <div class="text-sm" :class="step.done ? 'text-fg-muted line-through' : ''">{{ step.title }}</div>
            <div v-if="step.detail" class="mt-0.5 text-xs text-fg-subtle">{{ step.detail }}</div>
          </div>
        </li>
      </ul>
    </CardContent>
  </Card>
</template>