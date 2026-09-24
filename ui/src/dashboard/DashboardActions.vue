<script setup lang="ts">
import { AlignLeft, ListChecks, Route } from "@lucide/vue";
import { computed } from "vue";
import { RouterLink } from "vue-router";

import { Button } from "@/components/ui/button";
import { error, tasks } from "./state";
import { dashboardTaskTargets } from "./task-targets";

/**
 * The hero's actions. A task link is derived from the actual task list rather
 * than the counts, so one blocked task opens that task and a group opens the
 * filtered board.
 */
const targets = computed(() => dashboardTaskTargets(tasks.value ?? []));
</script>

<template>
  <template v-if="!error && tasks !== null">
    <Button v-if="targets.attention.count > 0" variant="attention" as-child>
      <RouterLink :to="targets.attention.href">
        <ListChecks data-icon="inline-start" />
        {{ targets.attention.count }} need{{
          targets.attention.count === 1 ? "s" : ""
        }}
        you
      </RouterLink>
    </Button>
    <Button v-if="targets.running.count > 0" variant="outline" as-child>
      <RouterLink :to="targets.running.href">
        <Route data-icon="inline-start" />
        {{ targets.running.count }} running
      </RouterLink>
    </Button>
    <Button variant="outline" as-child>
      <RouterLink to="/logs">
        <AlignLeft data-icon="inline-start" />
        Logs
      </RouterLink>
    </Button>
  </template>
</template>
