<script setup lang="ts">
import { ref } from "vue";
import { Play } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import NewTaskForm from "@/tasks/NewTaskForm.vue";

defineProps<{ workflow: string; disabled?: boolean }>();
const emit = defineEmits<{ started: [taskId: number] }>();
const open = ref(false);

function started(taskId: number): void {
  open.value = false;
  emit("started", taskId);
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogTrigger as-child>
      <Button type="button" size="sm" variant="outline" :disabled="disabled" :title="disabled ? 'Save your changes first' : undefined">
        <Play data-icon="inline-start" /> Run
      </Button>
    </DialogTrigger>
    <DialogContent class="sm:max-w-[640px]">
      <DialogHeader>
        <DialogTitle>Run {{ workflow }}</DialogTitle>
      </DialogHeader>
      <NewTaskForm v-if="open" :workflow="workflow" @started="started" @cancel="open = false" />
    </DialogContent>
  </Dialog>
</template>
