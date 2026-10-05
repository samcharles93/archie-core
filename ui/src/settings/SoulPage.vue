<script setup lang="ts">
import { computed, onMounted } from "vue";
import { storeToRefs } from "pinia";

import PageHeader from "@/base/PageHeader.vue";
import { Textarea } from "@/components/ui/textarea";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import DraftHint from "./DraftHint.vue";
import HistoryLink from "./HistoryLink.vue";

const KIND = "soul";

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "soul"));
const soul = computed(() => store.drafts[KIND]?.value as { text: string } | undefined);
const error = computed(() => store.stateFor(KIND).error);
const chars = computed(() => soul.value?.text.length ?? 0);
</script>

<template>
  <div>
    <PageHeader title="SOUL">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">
      {{ catalogError || error }}
    </p>

    <section v-if="soul" class="rounded-lg border border-border bg-card px-5 py-4">
      <div class="flex items-baseline justify-between gap-3">
        <label for="soul-text" class="text-[13px] font-medium">Identity</label>
        <DraftHint :kind="KIND" path="text" />
      </div>
      <p class="mt-1 text-xs text-fg-subtle">
        Archie's name, register and warmth, rendered into the chat prompt's identity slot. An empty
        value falls back to the SOUL file, then the shipped default. The invariant rules and tool
        inventory are unaffected.
      </p>
      <Textarea
        id="soul-text"
        v-model="soul.text"
        :rows="18"
        class="mt-3 font-mono text-[13px]"
        aria-label="SOUL text"
      />
      <p class="mt-1.5 text-xs text-fg-subtle">
        {{ chars.toLocaleString() }} chars · ~{{ Math.round(chars / 4).toLocaleString() }} tokens
      </p>
    </section>
  </div>
</template>
