<script setup lang="ts">
import { computed } from "vue";

import ConfigCard from "./ConfigCard.vue";
import ConfigList from "./ConfigList.vue";
import ConfigRow from "./ConfigRow.vue";
import { sectionRows, sectionsFor } from "./sections";
import { config, configEditable } from "./state";

/**
 * The generic schema rows for one page. The daemon serves one catalog of
 * sections (internal/webui/config_schema.go) and this renders the ones that
 * belong to the route it is handed, so a field added on the backend appears
 * here without a frontend change.
 */
const props = defineProps<{ ids: string[] }>();

const sections = computed(() => sectionsFor(config.value?.schema, props.ids));
</script>

<template>
  <ConfigCard v-for="section in sections" :key="section.id" :title="section.label">
    <ConfigList>
      <ConfigRow
        v-for="field in sectionRows(section)"
        :key="field.key"
        :label="field.label"
        :value="field.value"
        :field="field"
        :editable="configEditable"
      />
    </ConfigList>
  </ConfigCard>
</template>
