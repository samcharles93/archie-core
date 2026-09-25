<script setup lang="ts">
import { computed } from "vue";

import { StatusPill } from "@/components/ui/status-pill";
import {
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

// The schema the descriptor derives from its document type: the editor reads
// it for labels, hints, formatted placeholders and the shape of a new row. A
// descriptor that carries none reads as null, and the editor falls back to
// rendering the value alone.
const schema = computed(() => parseSchema(props.descriptor.schema_json));

// Edits land in the store's draft; the Settings save bar saves every edited
// resource together.
const draft = computed({
  get: () => store.drafts[props.descriptor.kind]?.value,
  set: (value) => {
    const current = store.drafts[props.descriptor.kind];
    if (current) current.value = value;
  },
});
const edited = computed(() => store.changesFor(props.descriptor.kind).length > 0);
</script>

<template>
  <ConfigCard :title="descriptor.title" :edited="edited">
    <template v-if="descriptor.apply_mode === 'restart-required'" #action>
      <StatusPill tone="warn">Applies after restart</StatusPill>
    </template>
    <div v-if="draft !== undefined" class="space-y-5">
      <StructuredValueEditor
        v-model="draft"
        :path="rootPath ?? descriptor.kind"
        :root-path="rootPath ?? descriptor.kind"
        :schema="schema"
      />
      <p v-if="state.error" class="text-sm text-destructive" role="alert">
        {{ state.error }}
      </p>
    </div>
  </ConfigCard>
</template>
