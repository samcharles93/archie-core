<script setup lang="ts">
import { Check, X } from "@lucide/vue";
import { computed, ref, watch } from "vue";

import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { dismissSetupComplete, setupPanelState, type SetupPanelState } from "./setup-preference";
import { setup } from "./state";

/**
 * The setup checklist, or its completion note.
 *
 * Which of the two this shows comes from the state machine rather than from
 * "is setup present at all": guarding on presence showed a 100% checklist with
 * every step struck through where the complete state belongs.
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

function dismiss() {
  dismissSetupComplete();
  panel.value = setupPanelState(setup.value);
}
</script>

<template>
  <Card v-if="panel.kind === 'incomplete'">
    <CardHeader>
      <CardTitle>Finish setting up</CardTitle>
      <CardDescription>Archie needs these before it can work on its own.</CardDescription>
      <CardAction>
        <span class="text-lg font-semibold text-primary">{{ pct }}%</span>
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

  <Card v-else-if="panel.kind === 'complete'">
    <CardHeader>
      <CardTitle>Setup complete</CardTitle>
      <CardDescription>Archie is configured and ready to work.</CardDescription>
      <CardAction class="flex items-center gap-2">
        <Check class="size-4 text-ok" />
        <Button variant="ghost" size="icon-sm" aria-label="Dismiss setup complete" @click="dismiss">
          <X />
        </Button>
      </CardAction>
    </CardHeader>
  </Card>
</template>
