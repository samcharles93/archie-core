<script setup lang="ts">
import { computed } from "vue";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import RuleEditor from "./RuleEditor.vue";
import { closeDraft, draft, saveDraft, saveError, saving } from "./event-type-state";

/**
 * One event-type editing session: naming a proposed group, creating a type
 * from a pasted example, or editing a type's name and rule. A refusal (an
 * overlapping rule, a taken name) is shown beside the Save button.
 */
const title = computed(() => {
  switch (draft.value?.mode) {
    case "proposal":
      return "Name event type";
    case "paste":
      return "Event type from payload";
    default:
      return "Edit event type";
  }
});

function onOpenChange(open: boolean) {
  if (!open) closeDraft();
}
</script>

<template>
  <Dialog :open="draft !== null" @update:open="onOpenChange">
    <DialogContent v-if="draft" class="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>Events on {{ draft.source || "the source" }} that match no type are never dispatched.</DialogDescription>
      </DialogHeader>

      <div class="flex flex-col gap-4">
        <div class="flex flex-col gap-2">
          <Label for="event-type-source">Source</Label>
          <Input id="event-type-source" v-model="draft.source" class="font-mono" :disabled="draft.mode !== 'paste'" />
        </div>
        <div class="flex flex-col gap-2">
          <Label for="event-type-name">Name</Label>
          <Input id="event-type-name" v-model="draft.name" placeholder="pull_request.opened" />
        </div>

        <template v-if="draft.mode === 'paste'">
          <div class="flex flex-col gap-2">
            <Label for="event-type-headers">Headers</Label>
            <Textarea id="event-type-headers" v-model="draft.headersText" class="min-h-20 font-mono text-xs" placeholder="X-GitHub-Event: pull_request" />
          </div>
          <div class="flex flex-col gap-2">
            <Label for="event-type-body">Payload</Label>
            <Textarea id="event-type-body" v-model="draft.body" class="min-h-40 font-mono text-xs" placeholder="{ }" />
          </div>
        </template>
        <RuleEditor v-else v-model="draft.rule" />
      </div>

      <Alert v-if="saveError" variant="destructive">
        <AlertDescription>{{ saveError }}</AlertDescription>
      </Alert>

      <DialogFooter>
        <Button variant="outline" @click="closeDraft">Cancel</Button>
        <Button :disabled="saving || !draft.name.trim() || !draft.source.trim()" @click="saveDraft">
          <Spinner v-if="saving" />
          Save
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
