<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";

import { Mic } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { applyCommand, isExactCommand } from "./commands";
import ChatSlashPalette from "./ChatSlashPalette.vue";
import {
  closeCommandMenu,
  commandMatches,
  commandSelection,
  commandSpecs,
  composerText,
  isCommandMenuOpen,
  isSending,
  moveCommandSelection,
  sendMessage,
  selectorData,
  sendVoice,
  stopTurn,
  syncCommandMenu,
} from "./state";

/**
 * The composer: the text, the palette that completes a slash command, and the
 * two actions. It stays writeable and focused while a turn is in flight --
 * stopping is not the only thing an operator may want to do next -- so Stop is
 * an addition to the row, never a replacement for Send.
 *
 * The textarea is stripped back to bare text inside the shell that carries the
 * border and the fill: the field's own chrome would draw a second box inside
 * the first. `bg-transparent!` beats the field's dark fill, which the dark
 * variant would otherwise re-apply over the shell. It grows with what is typed
 * and stops at `max-h-40`, so a long draft scrolls rather than eating the
 * transcript above it.
 */
const props = defineProps<{ open: boolean }>();

// Voice: recorded in the browser, transcribed by the server like a Telegram
// voice note. The mic shows only while the composer is empty.
const canRecord = computed(
  () => !!selectorData.value.voice_available && typeof MediaRecorder !== "undefined" && !!navigator.mediaDevices?.getUserMedia,
);
const recording = ref(false);
let recorder: MediaRecorder | null = null;
async function toggleRecording() {
  if (recorder) {
    recorder.stop();
    return;
  }
  const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
  const chunks: Blob[] = [];
  recorder = new MediaRecorder(stream);
  recorder.ondataavailable = (e) => chunks.push(e.data);
  recorder.onstop = () => {
    stream.getTracks().forEach((t) => t.stop());
    const clip = new Blob(chunks, { type: recorder?.mimeType || "audio/webm" });
    recorder = null;
    recording.value = false;
    if (clip.size) void sendVoice(clip);
  };
  recorder.start();
  recording.value = true;
}

const field = ref<{ $el: HTMLTextAreaElement } | null>(null);

function focus(): void {
  field.value?.$el?.focus();
}

defineExpose({ focus });

// The panel stays mounted so the session survives navigation, so opening it is
// a prop change rather than a mount: focus the composer on that edge.
watch(
  () => props.open,
  (open) => {
    if (open) void nextTick(focus);
  },
);

// A finished turn leaves the caret in the composer, where the next message is
// typed, rather than on the button that sent the last one.
watch(isSending, (sending, wasSending) => {
  if (wasSending && !sending) void nextTick(focus);
});

function onInput(event: Event): void {
  const input = event.target as HTMLTextAreaElement;
  composerText.value = input.value;
  syncCommandMenu(input.value, input.selectionStart ?? input.value.length);
}

function choose(index: number): void {
  const spec = commandMatches.value[index];
  const input = field.value?.$el;
  if (!spec || !input) return;
  // Completing the command replaces the token the caret sits in, which is
  // decided by the same rule that found the matches.
  const { text, cursor } = applyCommand(
    input.value,
    input.selectionStart ?? input.value.length,
    spec.command,
  );
  composerText.value = text;
  closeCommandMenu();
  void nextTick(() => {
    input.focus();
    input.setSelectionRange(cursor, cursor);
  });
}

function onKeydown(event: KeyboardEvent): void {
  const plain = !event.shiftKey && !event.ctrlKey && !event.metaKey;
  if (isCommandMenuOpen.value && commandMatches.value.length) {
    // A command typed in full is sent rather than completed, so Enter never
    // re-enters the command the palette was offering.
    if (
      event.key === "Enter" &&
      plain &&
      isExactCommand(commandSpecs.value, composerText.value)
    ) {
      event.preventDefault();
      closeCommandMenu();
      void sendMessage();
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      moveCommandSelection(1);
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      moveCommandSelection(-1);
      return;
    }
    if (event.key === "Enter" && plain) {
      event.preventDefault();
      choose(commandSelection.value);
      return;
    }
    if (event.key === "Escape") {
      // Consumed here: dismissing the palette must not also close the panel.
      event.preventDefault();
      closeCommandMenu();
      return;
    }
  }
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    void sendMessage();
  }
}
</script>

<template>
  <div class="border-t p-3">
    <div
      class="relative flex items-end gap-2 rounded-xl border border-border-strong bg-muted px-3 py-2.5"
    >
      <Textarea
        ref="field"
        :model-value="composerText"
        rows="3"
        placeholder="Message Archie, or type / for commands"
        aria-label="Message Archie"
        class="max-h-40 min-h-11 flex-1 resize-none border-0 bg-transparent! px-0 py-1 text-sm focus-visible:ring-0"
        @input="onInput"
        @keydown="onKeydown"
      />

      <ChatSlashPalette
        v-if="isCommandMenuOpen && commandMatches.length"
        :matches="commandMatches"
        :selection="commandSelection"
        @choose="choose"
      />

      <div class="flex flex-none items-center gap-2">
        <!-- Only while a turn is arriving: a Stop with nothing to stop is a
             button that lies about what the panel is doing. -->
        <Button v-if="isSending" variant="outline" size="sm" @click="stopTurn"
          >Stop</Button
        >
        <Button
          v-if="canRecord && !composerText.trim()"
          size="sm"
          :variant="recording ? 'destructive' : 'outline'"
          :disabled="isSending && !recording"
          :aria-label="recording ? 'Stop recording and send' : 'Record a voice message'"
          @click="toggleRecording"
          ><Mic data-icon="inline-start" />{{ recording ? "Send" : "" }}</Button
        >
        <Button
          v-else
          size="sm"
          :disabled="isSending || !composerText.trim()"
          @click="sendMessage()"
          >Send</Button
        >
      </div>
    </div>
  </div>
</template>
