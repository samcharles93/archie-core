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
    <div class="mb-5 flex flex-wrap items-start justify-between gap-5">
      <div>
        <h1 class="text-3xl font-semibold tracking-[-0.03em]">Field mappings</h1>
        <p class="mt-2 text-sm text-fg-muted">
          Bind named fields to JSON paths from a real captured event, ready for a playbook binding.
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <Button @click="startCreate">
          <Plus data-icon="inline-start" />
          New mapping
        </Button>
      </div>
    </div>

    <MappingActionError v-if="actionError" class="mb-4" :failure="actionError" />

    <MappingsTable />

    <MappingEditorDialog />
    <MappingDeleteDialog />
  </div>
</template>
