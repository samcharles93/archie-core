<script setup lang="ts">
import { RefreshCw } from "@lucide/vue";
import { onMounted, ref } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { api } from "@/lib/api";
import CuratorCard, { type Curator } from "./CuratorCard.vue";

/**
 * Curator observability (archie-core-1786637489932-6). Backed by GET
 * /api/curators, which reads the daemon's live curator registry: which curators
 * are registered, their point-in-time health, and their recent activity, with
 * the reason each action happened.
 */

const curators = ref<Curator[]>([]);
const loadError = ref<string | null>(null);

async function load() {
  try {
    const res = await api.curators<{ curators?: Curator[] }>();
    curators.value = res?.curators || [];
    loadError.value = null;
  } catch (err) {
    loadError.value = String((err as Error).message || err);
  }
}

onMounted(load);
</script>

<template>
  <div>
    <PageHeader
      title="Curators"
      subtitle="Background agents that maintain memory and skills: what ran, and why."
    >
      <Button variant="outline" @click="load">
        <RefreshCw data-icon="inline-start" />
        Refresh
      </Button>
    </PageHeader>

    <div class="grid grid-cols-1 gap-4 min-[1080px]:grid-cols-[repeat(auto-fit,minmax(340px,1fr))]">
      <Empty v-if="loadError">
        <EmptyHeader>
          <EmptyTitle>Cannot reach archied</EmptyTitle>
          <EmptyDescription>{{ loadError }}</EmptyDescription>
        </EmptyHeader>
      </Empty>
      <Empty v-else-if="!curators.length">
        <EmptyHeader>
          <EmptyTitle>No curators registered</EmptyTitle>
          <EmptyDescription>
            Curators are background agent loops that maintain memory and skills. None are registered on this daemon.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
      <CuratorCard v-for="c in curators" v-else :key="c.name" :curator="c" />
    </div>
  </div>
</template>
