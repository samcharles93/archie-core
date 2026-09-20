<script setup lang="ts">
import { onMounted, ref } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
import ChannelCard, { type Channel } from "./ChannelCard.vue";

/** The conversational front-ends Archie can be reached through. */

const channels = ref<Channel[]>([]);
const error = ref<string | null>(null);

async function load() {
  try {
    const res = await api.channels<{ channels?: Channel[] }>();
    channels.value = res?.channels || [];
    error.value = null;
  } catch (err) {
    error.value = String((err as Error).message || err);
  }
}

/** Reload, then re-read: the adapter's new state is what the card shows, so a
 * failed reload surfaces as the state it failed to leave. */
async function reload(id: string) {
  try {
    await api.channelReload(id);
  } catch {
    // Reported by the refreshed state below.
  }
  await load();
}

useLiveResource(null, () => void load());
onMounted(load);
</script>

<template>
  <div>
    <PageHeader
      title="Channels"
      subtitle="The conversational front-ends Archie can be reached through."
    />

    <div class="grid grid-cols-1 gap-4 min-[1080px]:grid-cols-[repeat(auto-fit,minmax(340px,1fr))]">
      <Empty v-if="error">
        <EmptyHeader>
          <EmptyTitle>Cannot reach archied</EmptyTitle>
          <EmptyDescription>{{ error }}</EmptyDescription>
        </EmptyHeader>
      </Empty>
      <Empty v-else-if="!channels.length">
        <EmptyHeader>
          <EmptyTitle>No channels configured</EmptyTitle>
          <EmptyDescription>
            Configure [chat.telegram] or [chat.webhook_addr] in config.toml to talk to Archie outside the dashboard.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
      <ChannelCard v-for="channel in channels" v-else :key="channel.id || channel.name" :channel="channel" @reload="reload" />
    </div>
  </div>
</template>
