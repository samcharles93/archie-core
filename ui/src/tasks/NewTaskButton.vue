<script setup lang="ts">
import { ref, watch } from "vue";
import { useRouter } from "vue-router";
import { Plus } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { api } from "@/lib/api";
import StartWorkForm from "@/workflows/StartWorkForm.vue";
import type { WorkflowDefinition } from "@/workflows/workflow-rows";

const router = useRouter();
const open = ref(false);
const definitions = ref<WorkflowDefinition[]>([]);
const error = ref("");

watch(open, async (isOpen) => {
  if (!isOpen) return;
  try {
    const data = await api.workflows<{ definitions?: WorkflowDefinition[] }>();
    definitions.value = data?.definitions ?? [];
    error.value = definitions.value.length ? "" : "No workflow definitions are served.";
  } catch (err) {
    error.value = String((err as Error).message || err);
  }
});

function started(taskId: number) {
  open.value = false;
  void router.push(`/tasks/${taskId}`);
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogTrigger as-child>
      <Button size="sm">
        <Plus data-icon="inline-start" /> New task
      </Button>
    </DialogTrigger>
    <DialogContent class="sm:max-w-[640px]">
      <DialogHeader>
        <DialogTitle>New task</DialogTitle>
        <DialogDescription>Enters Archie's admitted task queue.</DialogDescription>
      </DialogHeader>
      <p v-if="error" class="text-sm text-danger" role="alert">{{ error }}</p>
      <StartWorkForm
        v-else-if="definitions.length"
        :definitions="definitions"
        @started="started"
      />
    </DialogContent>
  </Dialog>
</template>
