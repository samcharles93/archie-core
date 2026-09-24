<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import { api } from "@/lib/api";
import StructuredResourceCard from "@/settings/StructuredResourceCard.vue";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import { useLiveResource } from "@/stores/live-updates";
import ChannelCard, { type Channel } from "./ChannelCard.vue";

/** The conversational front-ends Archie can be reached through. */

const channels = ref<Channel[]>([]);
const error = ref<string | null>(null);
const controlPlane = useControlPlaneStore();
const { catalog } = storeToRefs(controlPlane);
const settings = computed(() => resourcesForPage(catalog.value, "channels"));

async function load() {
  try {
    const res = await api.channels<{ channels?: Channel[] }>();
    channels.value = res?.channels || [];
    error.value = null;
  } catch (err) {
    error.value = String((err as Error).message || err);
  }
}

useLiveResource(null, () => void load());
onMounted(() => Promise.all([load(), controlPlane.load()]));
</script>

<template>
  <div>
    <PageHeader title="Channels" />

    <div
      class="grid grid-cols-1 gap-4 min-[1080px]:grid-cols-[repeat(auto-fit,minmax(340px,1fr))]"
    >
      <Empty v-if="error">
        <EmptyHeader>
          <EmptyTitle>Cannot reach archied</EmptyTitle>
          <EmptyDescription>{{ error }}</EmptyDescription>
        </EmptyHeader>
      </Empty>
      <Empty v-else-if="!channels.length">
        <EmptyHeader>
          <EmptyTitle>No channels configured</EmptyTitle>
          <EmptyDescription>Add one below.</EmptyDescription>
        </EmptyHeader>
      </Empty>
      <ChannelCard
        v-for="channel in channels"
        v-else
        :key="channel.id || channel.name"
        :channel="channel"
      />
    </div>
    <StructuredResourceCard
      v-for="descriptor in settings"
      :key="descriptor.kind"
      :descriptor="descriptor"
    />
  </div>
</template>
