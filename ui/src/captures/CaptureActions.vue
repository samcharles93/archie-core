<script setup lang="ts">
import { RefreshCw } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { load } from "./state";

/** Read the list again, for an operator who does not want to wait for the
 * stream to invalidate it. */
</script>

<template>
  <Button variant="outline" @click="load">
    <RefreshCw data-icon="inline-start" />
    Refresh
  </Button>
</template>
