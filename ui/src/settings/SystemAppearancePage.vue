<script setup lang="ts">
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  type Density,
  type Motion,
  type Theme,
  useAppearanceStore,
} from "@/stores/appearance";
import ConfigCard from "./ConfigCard.vue";

const appearance = useAppearanceStore();
const { density, motion, theme, tooltipsEnabled } = storeToRefs(appearance);
const { setDensity, setMotion, setTheme, setTooltips } = appearance;

/**
 * Appearance: how the dashboard looks, on this browser.
 *
 * These preferences are browser-local and apply immediately. They are grouped
 * into one bounded surface so labels and controls can be read together instead
 * of making the operator scan opposite edges of several full-width cards.
 */
function chooseTheme(value: unknown): void {
  if (value === "system" || value === "light" || value === "dark") setTheme(value as Theme);
}

function chooseDensity(value: unknown): void {
  if (value === "comfortable" || value === "compact") setDensity(value as Density);
}

function chooseMotion(value: unknown): void {
  if (value === "system" || value === "reduced") setMotion(value as Motion);
}
</script>

<template>
  <div>
    <PageHeader title="Appearance" subtitle="How this dashboard looks and responds in this browser." />

    <ConfigCard title="Dashboard preferences" description="Saved only in this browser and applied immediately.">
      <div class="grid gap-8 min-[700px]:grid-cols-2 min-[1100px]:grid-cols-4">
        <section class="flex min-w-0 flex-col items-start">
          <div>
            <h2 id="appearance-theme" class="text-sm font-medium">Theme</h2>
            <p class="mt-1 text-sm text-muted-foreground">Follow your device, or keep Archie light or dark.</p>
          </div>
          <ToggleGroup
            type="single"
            variant="outline"
            :model-value="theme"
            class="mt-4"
            aria-labelledby="appearance-theme"
            @update:model-value="chooseTheme"
          >
            <ToggleGroupItem value="system">System</ToggleGroupItem>
            <ToggleGroupItem value="light">Light</ToggleGroupItem>
            <ToggleGroupItem value="dark">Dark</ToggleGroupItem>
          </ToggleGroup>
        </section>

        <section class="flex min-w-0 flex-col items-start">
          <div>
            <h2 id="appearance-density" class="text-sm font-medium">Density</h2>
            <p class="mt-1 text-sm text-muted-foreground">Use relaxed spacing, or fit more work on screen.</p>
          </div>
          <ToggleGroup
            type="single"
            variant="outline"
            :model-value="density"
            class="mt-4"
            aria-labelledby="appearance-density"
            @update:model-value="chooseDensity"
          >
            <ToggleGroupItem value="comfortable">Comfortable</ToggleGroupItem>
            <ToggleGroupItem value="compact">Compact</ToggleGroupItem>
          </ToggleGroup>
        </section>

        <section class="flex min-w-0 flex-col items-start">
          <div>
            <h2 id="appearance-motion" class="text-sm font-medium">Motion</h2>
            <p class="mt-1 text-sm text-muted-foreground">Follow your device, or reduce interface animation here.</p>
          </div>
          <ToggleGroup
            type="single"
            variant="outline"
            :model-value="motion"
            class="mt-4"
            aria-labelledby="appearance-motion"
            @update:model-value="chooseMotion"
          >
            <ToggleGroupItem value="system">System</ToggleGroupItem>
            <ToggleGroupItem value="reduced">Reduced</ToggleGroupItem>
          </ToggleGroup>
        </section>

        <section class="flex min-w-0 flex-col items-start">
          <div>
            <h2 id="appearance-tooltips" class="text-sm font-medium">Tooltips</h2>
            <p class="mt-1 text-sm text-muted-foreground">Show extra context when controls are hovered or focused.</p>
          </div>
          <Switch
            :model-value="tooltipsEnabled"
            class="mt-4"
            aria-labelledby="appearance-tooltips"
            @update:model-value="setTooltips"
          />
        </section>
      </div>
    </ConfigCard>
  </div>
</template>
