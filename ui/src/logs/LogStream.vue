<script setup lang="ts">
import { Card, CardContent, CardHeader } from "@/components/ui/card";

import LogFilters from "./LogFilters.vue";
import LogList from "./LogList.vue";
import {serviceErrors} from "./state";
import LogStatus from "./LogStatus.vue";

/** The log surface: the filters and the stream's own state over the lines. */
</script>

<template>
  <!-- gap-0 so the header's rule sits on the list rather than a card gap above
       it; the rows carry their own separators. -->
  <Card class="gap-0">
    <CardHeader
      class="flex flex-wrap items-center justify-between gap-3 border-b"
    >
      <LogFilters />
      <LogStatus />
    </CardHeader>
    <CardContent class="px-0">
      <p v-for="(error,service) in serviceErrors" :key="service" role="alert" class="px-3 py-2 text-sm text-danger">{{ service }}: {{ error }}</p>
      <LogList />
    </CardContent>
  </Card>
</template>
