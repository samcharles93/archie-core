<script setup lang="ts">
import { Paperclip } from "@lucide/vue";

import { mediaDetail, mediaLabel } from "./attachment";
import type { ChatMedia } from "./state";

/**
 * One attachment a message carried, as the transcript shows it.
 *
 * The bytes are gone by design — they are turn-scoped and are stripped before
 * the record is stored — so this is a labelled chip rather than a preview. It
 * links out only when the attachment itself carried a URL; a platform file id
 * is deliberately never rendered, because it is a handle rather than an
 * address and a chip that 404s is worse than no chip.
 */
defineProps<{ attachment: ChatMedia }>();
</script>

<template>
  <component
    :is="attachment.url ? 'a' : 'span'"
    :href="attachment.url"
    :target="attachment.url ? '_blank' : undefined"
    :rel="attachment.url ? 'noopener noreferrer' : undefined"
    :title="mediaDetail(attachment)"
    class="inline-flex max-w-full items-center gap-1.5 rounded-full border border-border bg-muted px-2.5 py-1 text-xs font-medium text-muted-foreground"
  >
    <Paperclip aria-hidden="true" class="size-3.5 shrink-0" />
    <span class="truncate">{{ mediaLabel(attachment) }}</span>
  </component>
</template>
