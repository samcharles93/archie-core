<script setup lang="ts">
import { Plus } from "@lucide/vue";
import { onMounted, ref } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useLiveResource } from "@/stores/live-updates";
import BindingEditor from "./BindingEditor.vue";
import BindingsTable from "./BindingsTable.vue";
import DeleteBindingDialog from "./DeleteBindingDialog.vue";
import type { Binding, BindingDraft } from "./binding-draft";
import { useBindings } from "./use-bindings";

/**
 * Playbook bindings (t2db.8): tie a matcher + payload mapping + workflow
 * together, with a draft -> pending_approval -> armed state machine so nothing
 * self-arms. See docs/architecture/bindings.md and
 * docs/prds/payload-field-mapping.md. Owner/Repo optionally pin a multi-repo
 * deployment's dispatch target (archie-core-t2db.8's backend fix, commit
 * ac42f64).
 */

const {
  bindings,
  mappings,
  workflows,
  failure,
  actionFailure,
  saving,
  saveFailure,
  load,
  resetSaveFailure,
  save,
  approve,
  remove,
} = useBindings();

const editorOpen = ref(false);
// null is a new binding; a binding is an edit.
const editing = ref<Binding | null>(null);
const deleting = ref<Binding | null>(null);

useLiveResource(null, () => void load());
onMounted(load);

function startCreate(): void {
  editing.value = null;
  resetSaveFailure();
  editorOpen.value = true;
}

function startEdit(binding: Binding): void {
  editing.value = binding;
  resetSaveFailure();
  editorOpen.value = true;
}

async function handleSave(draft: BindingDraft): Promise<void> {
  // The editor stays open on failure so the server's reason and the fields it
  // is about are still on screen together.
  if (await save(draft)) editorOpen.value = false;
}

async function handleDelete(binding: Binding): Promise<void> {
  deleting.value = null;
  await remove(binding);
}
</script>

<template>
  <div>
    <PageHeader title="Playbook bindings">
      <Button v-if="!failure" @click="startCreate">
        <Plus data-icon="inline-start" />
        New binding
      </Button>
    </PageHeader>

    <!-- A mutation that failed leaves the list on screen accurate, so its
         failure sits beside the list rather than replacing it. -->
    <Alert v-if="actionFailure" variant="destructive" class="mb-4">
      <AlertTitle>That did not take effect</AlertTitle>
      <AlertDescription>{{ actionFailure.message }}</AlertDescription>
    </Alert>

    <BindingsTable
      :bindings="bindings"
      :failure="failure"
      @edit="startEdit"
      @approve="approve"
      @delete="deleting = $event"
    />

    <BindingEditor
      v-model:open="editorOpen"
      :binding="editing"
      :mappings="mappings"
      :workflows="workflows"
      :saving="saving"
      :error="saveFailure"
      @save="handleSave"
    />

    <DeleteBindingDialog :binding="deleting" @confirm="handleDelete" @cancel="deleting = null" />
  </div>
</template>
