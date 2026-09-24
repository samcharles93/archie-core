<script setup lang="ts">
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import MappingActionError from "./MappingActionError.vue";
import MappingFieldsTable from "./MappingFieldsTable.vue";
import MappingForm from "./MappingForm.vue";
import PayloadTree from "./PayloadTree.vue";
import {
  actionError,
  addField,
  closeEditor,
  draft,
  editorOpen,
  runPreview,
  saveMapping,
  saving,
} from "./state";

/**
 * One editing session, in an overlay: the mapping's own fields, the fields
 * bound to payload paths, and the example event they were bound from.
 *
 * The payload panel is the editor's primary control rather than a preview of
 * one: a field is bound by clicking a value that really is in a captured event,
 * so no JSON path is ever typed by hand.
 */
function onOpenChange(open: boolean) {
  if (!open) closeEditor();
}
</script>

<template>
  <Dialog :open="editorOpen" @update:open="onOpenChange">
    <DialogContent class="max-h-[85vh] overflow-y-auto sm:max-w-3xl">
      <DialogHeader>
        <DialogTitle>{{
          draft.id === null ? "New mapping" : "Edit mapping"
        }}</DialogTitle>
        <DialogDescription>
          Bind named fields to JSON paths from a real captured event, ready for
          a playbook binding.
        </DialogDescription>
      </DialogHeader>

      <Card>
        <CardContent>
          <MappingForm />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Bound fields</CardTitle>
        </CardHeader>
        <CardContent>
          <MappingFieldsTable />
        </CardContent>
        <CardFooter>
          <Button variant="outline" @click="runPreview">Preview</Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Payload — click a value to bind it</CardTitle>
        </CardHeader>
        <CardContent>
          <Alert v-if="draft.payloadError" variant="destructive">
            <AlertDescription>{{ draft.payloadError }}</AlertDescription>
          </Alert>
          <PayloadTree
            v-else-if="draft.payload !== null"
            :value="draft.payload"
            @pick="addField"
          />
          <p v-else class="text-sm text-fg-muted">
            Pick a captured event above to see its payload.
          </p>
        </CardContent>
      </Card>

      <!-- Beside the buttons, not at the top: the dialog scrolls, and Save is
           clicked at the bottom, where a refusal must be seen. -->
      <MappingActionError v-if="actionError" :failure="actionError" />

      <DialogFooter>
        <Button variant="outline" @click="closeEditor">Cancel</Button>
        <Button :disabled="saving" @click="saveMapping">
          <Spinner v-if="saving" data-icon="inline-start" />
          {{ saving ? "Saving..." : "Save" }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
