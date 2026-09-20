<script setup lang="ts">
import { Switch } from "@/components/ui/switch";
import { setTooltips, tooltipsEnabled } from "@/lib/preferences";
import ConfigCard from "./ConfigCard.vue";

</script>

<template>
  <ConfigCard title="Tooltips" description="Shown on hover, and the only place a clamped cell's full text lives.">
    <div class="flex justify-end">
      <Switch aria-label="Tooltips" :model-value="tooltipsEnabled" @update:model-value="setTooltips" />
    </div>
  </ConfigCard>
</template>
