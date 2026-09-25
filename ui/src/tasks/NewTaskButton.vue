<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";
import { Plus } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import NewTaskForm from "./NewTaskForm.vue";

const router = useRouter();
const open = ref(false);

function started(taskId: number) {
  open.value = false;
  void router.push(`/tasks/${taskId}`);
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogTrigger as-child>
      <Button size="sm"><Plus data-icon="inline-start" /> New task</Button>
    </DialogTrigger>
    <DialogContent class="sm:max-w-[640px]">
      <DialogHeader>
        <DialogTitle>New task</DialogTitle>
      </DialogHeader>
      <NewTaskForm v-if="open" @started="started" @cancel="open = false" />
    </DialogContent>
  </Dialog>
</template>
