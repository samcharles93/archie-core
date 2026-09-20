<script setup lang="ts">
import { MessageCirclePlus } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { panelTitle } from "./session";
import { currentSession, newChat, sessions } from "./state";

/**
 * The panel's head: which conversation is open, and the one action that always
 * applies to it.
 *
 * There is no close button. The launcher stays visible while the panel is open
 * and is the single control that opens and closes it, so a second one here
 * would be a duplicate that can drift out of step.
 */
</script>

<template>
  <div class="flex items-center justify-between gap-2 border-b px-4 py-3">
    <strong class="truncate text-sm font-semibold">{{ panelTitle(sessions, currentSession) }}</strong>
    <Button variant="ghost" size="icon-sm" class="flex-none" title="New chat" aria-label="New chat" @click="newChat">
      <MessageCirclePlus />
    </Button>
  </div>
</template>
