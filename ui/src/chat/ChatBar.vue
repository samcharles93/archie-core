<script setup lang="ts">
import ChatSessionList from "./ChatSessionList.vue";
import ChatSettings from "./ChatSettings.vue";

/**
 * One row for everything that is not the conversation: which conversation is
 * open, and the controls that are consulted rather than used constantly.
 */
</script>

<template>
  <div class="flex items-center gap-2 border-b px-3 py-2">
    <ChatSessionList />
    <ChatSettings />
  </div>
</template>
