<script setup lang="ts">
import { Plus } from "@lucide/vue";
import { onMounted } from "vue";

import { Button } from "@/components/ui/button";
import MappingActionError from "./MappingActionError.vue";
import MappingDeleteDialog from "./MappingDeleteDialog.vue";
import MappingEditorDialog from "./MappingEditorDialog.vue";
import MappingsTable from "./MappingsTable.vue";
import { actionError, loadMappings, startCreate } from "./state";

/**
 * Payload field mapping (t2db.3): bind named fields to JSON paths inside a real
 * captured event, preview the resolution before saving, and manage saved
 * mappings. See docs/prds/payload-field-mapping.md. Binding a mapping and a
 * matcher to a workflow (t2db.4) is a separate, not-yet-built page -- this one
 * only produces the mapping entity and its preview.
 *
 * Composes the list and the two overlays; it holds no state of its own.
 */
onMounted(loadMappings);
</script>

<template>
  <div>
    <!-- The tab owns its own action row; the page header names the page. -->
    <div class="mb-4 flex flex-wrap items-center justify-end gap-2">
      <Button @click="startCreate">
        <Plus data-icon="inline-start" />
        New mapping
      </Button>
    </div>

    <MappingActionError v-if="actionError" class="mb-4" :failure="actionError" />

    <MappingsTable />

    <MappingEditorDialog />
    <MappingDeleteDialog />
  </div>
</template>
