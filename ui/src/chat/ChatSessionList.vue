<script setup lang="ts">
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { sessionTitle } from "./session";
import { currentSession, selectSession, sessions } from "./state";

/**
 * Which conversation is open, and how to switch. A select rather than a list:
 * the panel is small, and choosing between a handful of conversations is a
 * control, not a place to be.
 *
 * reka-ui refuses an empty string as an item value -- it is how the component
 * spells "nothing selected" -- and a session id is never empty, so the ids pass
 * through unchanged.
 */
</script>

<template>
  <div class="min-w-0 flex-1">
    <Select
      :model-value="currentSession"
      :disabled="!sessions.length"
      @update:model-value="selectSession(String($event))"
    >
      <SelectTrigger size="sm" class="w-full" aria-label="Conversation">
        <SelectValue placeholder="No conversations yet" />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          <SelectItem
            v-for="session in sessions"
            :key="session.session_id"
            :value="session.session_id"
          >
            {{ sessionTitle(session) }}
          </SelectItem>
        </SelectGroup>
      </SelectContent>
    </Select>
  </div>
</template>
