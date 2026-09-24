<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import {
  cloneControlPlaneValue,
  useControlPlaneStore,
  type ResourceDescriptor,
} from "@/stores/control-plane";
import ConfigCard from "./ConfigCard.vue";
import StructuredValueEditor from "./StructuredValueEditor.vue";
import { parseSchema } from "./resource-schema";

const props = defineProps<{
  descriptor: ResourceDescriptor;
  rootPath?: string;
}>();
const store = useControlPlaneStore();
const state = computed(() => store.stateFor(props.descriptor.kind));
const draft = ref<unknown>({});

// The schema the descriptor derives from its document type: the editor reads
// it for labels, hints, formatted placeholders and the shape of a new row. A
// descriptor that carries none reads as null, and the editor falls back to
// rendering the value alone.
const schema = computed(() => parseSchema(props.descriptor.schema_json));

watch(
  () => state.value.resource,
  (resource) => {
    if (resource) draft.value = cloneControlPlaneValue(resource.value);
  },
  { immediate: true },
);

async function save(): Promise<void> {
  await store.replace(
    props.descriptor.kind,
    cloneControlPlaneValue(draft.value),
  );
}
</script>

<template>
  <ConfigCard :title="descriptor.title">
    <form class="space-y-5" @submit.prevent="save">
      <StructuredValueEditor
        v-model="draft"
        :path="rootPath ?? descriptor.kind"
        :root-path="rootPath ?? descriptor.kind"
        :schema="schema"
      />
      <p v-if="state.error" class="text-sm text-destructive" role="alert">
        {{ state.error }}
      </p>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-2">
          <Badge :variant="state.stream === 'live' ? 'ok' : 'warn'">{{
            state.stream === "live" ? "Live" : "Connecting"
          }}</Badge>
          <Badge
            v-if="descriptor.apply_mode === 'restart-required'"
            variant="warn"
            >Restart required</Badge
          >
          <span v-if="state.resource" class="text-xs text-muted-foreground"
            >Version {{ state.resource.version }}</span
          >
        </div>
        <Button type="submit" :disabled="state.saving || state.loading">
          <Spinner v-if="state.saving" data-icon="inline-start" /> Save changes
        </Button>
      </div>
    </form>
  </ConfigCard>
</template>
