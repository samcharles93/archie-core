<script setup lang="ts">
import { History } from "@lucide/vue";

import { Button } from "@/components/ui/button";

/** Opens Settings > History filtered to these resource kinds. */
const props = defineProps<{ kinds: string[] }>();
</script>

<template>
  <Button variant="ghost" size="sm" as-child>
    <RouterLink :to="{ path: '/settings/history', query: { section: props.kinds.join(',') } }">
      <History data-icon="inline-start" /> History
    </RouterLink>
  </Button>
</template>
