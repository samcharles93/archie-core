<script setup lang="ts">
import { ref } from "vue";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { answerAsk, type ChatAsk } from "./state";

defineProps<{ ask: ChatAsk }>();

const reply = ref("");
function sendReply() {
  const text = reply.value.trim();
  if (!text) return;
  reply.value = "";
  void answerAsk(text);
}
</script>

<template>
  <div class="mt-2 rounded-lg border border-border bg-card p-3 text-sm" role="group" :aria-label="ask.prompt">
    <template v-if="ask.kind === 'approval'">
      <p class="font-medium">Allow {{ ask.prompt }}?</p>
      <p v-if="ask.detail" class="mt-1 font-mono text-xs break-all text-fg-muted">{{ ask.detail }}</p>
      <div class="mt-3 flex flex-wrap gap-2">
        <Button size="sm" @click="answerAsk('approve')">Approve</Button>
        <Button size="sm" variant="outline" @click="answerAsk('always')">Always this session</Button>
        <Button size="sm" variant="ghost" class="text-danger" @click="answerAsk('deny')">Deny</Button>
      </div>
    </template>

    <template v-else-if="ask.kind === 'picker'">
      <p class="font-medium">{{ ask.prompt }}</p>
      <div class="mt-3 flex flex-wrap gap-2">
        <Button v-for="choice in ask.choices" :key="choice.id" size="sm" variant="outline" @click="answerAsk(choice.id)">
          {{ choice.label }}
        </Button>
      </div>
    </template>

    <template v-else>
      <p class="font-medium">{{ ask.prompt }}</p>
      <div v-if="ask.choices?.length" class="mt-3 flex flex-wrap gap-2">
        <Button v-for="choice in ask.choices" :key="choice.id" size="sm" variant="outline" @click="answerAsk(choice.label)">
          {{ choice.label }}
        </Button>
      </div>
      <form class="mt-3 flex gap-2" @submit.prevent="sendReply">
        <Input v-model="reply" class="flex-1" placeholder="Your answer" aria-label="Your answer" />
        <Button type="submit" size="sm" :disabled="!reply.trim()">Send</Button>
      </form>
    </template>
  </div>
</template>
