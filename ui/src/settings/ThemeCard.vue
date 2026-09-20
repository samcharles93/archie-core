<script setup lang="ts">
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { setTheme, theme, type Theme } from "@/lib/theme";
import ConfigCard from "./ConfigCard.vue";

/**
 * The theme control, moved off the topbar icon and onto a page of its own so
 * the chrome carries no preference.
 *
 * It writes through lib/theme, which sets the attribute index.html reads before
 * the first paint and stores the choice in this browser: there is no
 * server-side appearance to save, and nothing here to submit. The density and
 * motion preferences this page exists for are not built yet
 * (docs/prds/dashboard-navigation-groups.md).
 */
function apply(value: unknown): void {
  if (value === "light" || value === "dark") setTheme(value as Theme);
}
</script>

<template>
  <ConfigCard title="Theme">
    <div class="flex justify-end">
      <ToggleGroup :model-value="theme" aria-label="Theme" @update:model-value="apply">
        <ToggleGroupItem value="dark">Dark</ToggleGroupItem>
        <ToggleGroupItem value="light">Light</ToggleGroupItem>
      </ToggleGroup>
    </div>
  </ConfigCard>
</template>
