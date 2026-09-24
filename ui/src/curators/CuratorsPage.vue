<script setup lang="ts">
import { onMounted, ref } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
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

useLiveResource("curators", () => void load());
onMounted(load);
</script>

<template>
  <div>
    <PageHeader title="Curators" />

    <div
      class="grid grid-cols-1 gap-4 lg:grid-cols-[repeat(auto-fit,minmax(340px,1fr))]"
    >
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
            Curators are background agent loops that maintain memory and skills.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
      <CuratorCard v-for="c in curators" v-else :key="c.name" :curator="c" />
    </div>
  </div>
</template>
